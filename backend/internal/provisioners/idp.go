// Package provisioners defines the connector boundary between the HR module
// and external systems (identity provider, workspace, etc.). HR only ever
// talks to interfaces defined here. Real implementations (Keycloak IdP,
// Google Workspace, …) live behind these interfaces and are swapped in
// without touching service code. See ADR-0002.
package provisioners

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

// IdPUser is the minimal shape HR hands to the identity provider.
type IdPUser struct {
	HRPersonID uuid.UUID
	// ExternalID is the account HR already linked to this person. When it
	// still exists it is updated in place (its username is never changed);
	// otherwise a new account is created with Username.
	ExternalID  string
	Username    string
	DisplayName string
	FirstName   string
	LastName    string
	Email       string
	// Attributes are extra profile fields (nickname, phone, …). Empty values
	// remove the attribute.
	Attributes map[string]string
	Groups     []string
	// SendPasswordSetup asks the IdP to email a brand-new user a link to set
	// their own password instead of HR handing out a temporary one.
	SendPasswordSetup bool
}

// IdPResult is what the provider returns on success.
type IdPResult struct {
	// ExternalID is the IdP's native id for the user (sub / uuid / …).
	ExternalID string
	// GroupsApplied echoes the groups actually set.
	GroupsApplied []string
	// Created is true when the IdP user did not exist before this call.
	Created bool
	// PasswordSetupEmailSent is true when the IdP emailed a set-password link.
	PasswordSetupEmailSent bool
	// PasswordSetupError explains why the email could not be sent (the
	// realm needs SMTP). HR never sets a password; resend the email instead.
	PasswordSetupError string
}

// PasswordSetupSender emails an existing user a new set-password link.
type PasswordSetupSender interface {
	SendPasswordSetup(ctx context.Context, userID string) error
}

// LinkOnlyReporter is implemented by provisioners that can run link-only
// (IDP_PROVISIONER_MODE=link).
type LinkOnlyReporter interface {
	LinkOnly() bool
}

// UserInfo is one account in the IdP (review queue scan).
type UserInfo struct {
	ID            string            `json:"id"`
	Username      string            `json:"username"`
	Email         string            `json:"email"`
	EmailVerified bool              `json:"email_verified"`
	FirstName     string            `json:"first_name"`
	LastName      string            `json:"last_name"`
	Enabled       bool              `json:"enabled"`
	Attributes    map[string]string `json:"attributes"`
	CreatedAt     *time.Time        `json:"created_at,omitempty"`
}

// UserScanner pages through every account in the IdP.
type UserScanner interface {
	ScanUsers(ctx context.Context, fn func(UserInfo) error) error
}

// GroupEnsurer is implemented by IdPs that can create groups ahead of any
// user being provisioned (departments create theirs on save).
type GroupEnsurer interface {
	EnsureGroups(ctx context.Context, names []string) error
}

// GroupMember is a user listed inside an IdP group.
type GroupMember struct {
	ID        string `json:"id"`
	Username  string `json:"username"`
	Email     string `json:"email,omitempty"`
	FirstName string `json:"first_name,omitempty"`
	LastName  string `json:"last_name,omitempty"`
	Enabled   bool   `json:"enabled"`
}

// GroupInfo is one IdP group with its direct members.
type GroupInfo struct {
	ID      string        `json:"id"`
	Name    string        `json:"name"`
	Path    string        `json:"path"`
	Members []GroupMember `json:"members"`
}

// Directory reads groups from the IdP (HR's read-only Keycloak view).
type Directory interface {
	ListGroups(ctx context.Context) ([]GroupInfo, error)
}

// ErrAccountNotFound means the IdP has no user with the stored id (deleted
// in the IdP, or created by a different/fake IdP).
var ErrAccountNotFound = errors.New("account not found in the identity provider")

// UsernameTakenError means a new account would reuse a username that
// belongs to someone else in the IdP.
type UsernameTakenError struct{ Username string }

func (e *UsernameTakenError) Error() string {
	return fmt.Sprintf("the username %q is already used by another account", e.Username)
}

// AccountInfo is the login state of an IdP user.
type AccountInfo struct {
	Username string `json:"username"`
	Enabled  bool   `json:"enabled"`
}

// Profile is what HR keeps in sync on an existing account. A non-empty
// Username renames the login.
type Profile struct {
	Username   string
	Email      string
	FirstName  string
	LastName   string
	Attributes map[string]string
}

// AccountManager changes an existing IdP account after onboarding
// (access changes, offboarding). Methods return ErrAccountNotFound when the
// user no longer exists.
type AccountManager interface {
	GetAccount(ctx context.Context, userID string) (*AccountInfo, error)
	UpdateMembership(ctx context.Context, userID string, add, remove []string) error
	SetEnabled(ctx context.Context, userID string, enabled bool) error
	UpdateProfile(ctx context.Context, userID string, p Profile) error
	DeleteAccount(ctx context.Context, userID string) error
}

// UsernameDirectory finds out whether a login name is already in use.
type UsernameDirectory interface {
	// UsernameOwner reports whether an account uses the username and, if so,
	// the HR person id stored on it ("" for accounts HR did not create).
	UsernameOwner(ctx context.Context, username string) (owner string, found bool, err error)
}

// IdPProvisioner is the connector the HR service calls. FakeIdP is used in
// tests; KeycloakIdP talks to the shared suite Keycloak in AUTH stack mode.
type IdPProvisioner interface {
	// Provision creates or updates the user in the IdP and syncs groups.
	// It must be idempotent on HRPersonID so retries are safe.
	Provision(ctx context.Context, u IdPUser) (IdPResult, error)
}

// -----------------------------------------------------------------------------
// Fake implementation — in-memory, deterministic, no network.
// -----------------------------------------------------------------------------

// FakeIdP is a stub IdPProvisioner used in local dev and tests. It records
// every provision call and returns a stable fake external id derived from
// the HR person id. Replace with a Keycloak-backed implementation when
// identity-first auth lands.
type FakeIdP struct {
	logger zerolog.Logger

	mu          sync.Mutex
	calls       []IdPUser
	memberships []MembershipChange
	enabled     map[string]bool
	setupMails  []string
	// Users are returned by ScanUsers (tests).
	Users []UserInfo
	// FailProvision makes Provision fail with this error (tests).
	FailProvision error
}

// MembershipChange is one UpdateMembership call seen by FakeIdP.
type MembershipChange struct {
	UserID string
	Add    []string
	Remove []string
}

// NewFakeIdP returns a FakeIdP that logs each call via the provided logger.
func NewFakeIdP(logger zerolog.Logger) *FakeIdP {
	return &FakeIdP{logger: logger.With().Str("component", "fake_idp").Logger()}
}

// Provision records the call and returns a deterministic ExternalID.
func (f *FakeIdP) Provision(ctx context.Context, u IdPUser) (IdPResult, error) {
	if ctx.Err() != nil {
		return IdPResult{}, ctx.Err()
	}
	if u.HRPersonID == uuid.Nil {
		return IdPResult{}, fmt.Errorf("fake idp: HRPersonID required")
	}
	if u.Username == "" {
		return IdPResult{}, fmt.Errorf("fake idp: Username required")
	}

	f.mu.Lock()
	f.calls = append(f.calls, u)
	fail := f.FailProvision
	f.mu.Unlock()
	if fail != nil {
		return IdPResult{}, fail
	}

	// Deterministic external id: "idp:<uuid>" so tests can assert exact
	// values without pre-seeding anything.
	ext, created := u.ExternalID, false
	if ext == "" || (!strings.HasPrefix(ext, "idp:") && !f.scanned(ext)) {
		ext, created = "idp:"+u.HRPersonID.String(), true
	}
	f.logger.Info().
		Str("hr_person_id", u.HRPersonID.String()).
		Str("username", u.Username).
		Strs("groups", u.Groups).
		Str("external_id", ext).
		Time("at", time.Now().UTC()).
		Msg("fake idp provision")

	// Copy groups so the caller cannot mutate FakeIdP's stored call.
	applied := append([]string(nil), u.Groups...)
	return IdPResult{ExternalID: ext, GroupsApplied: applied, Created: created, PasswordSetupEmailSent: created && u.SendPasswordSetup}, nil
}

// EnsureGroups logs the groups that a real IdP would create.
func (f *FakeIdP) EnsureGroups(_ context.Context, names []string) error {
	f.logger.Info().Strs("groups", names).Msg("fake idp ensure groups")
	return nil
}

// ListGroups returns the groups seen in provision calls (no members).
func (f *FakeIdP) ListGroups(_ context.Context) ([]GroupInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	seen := map[string]bool{}
	out := []GroupInfo{}
	for _, c := range f.calls {
		for _, g := range c.Groups {
			if !seen[g] {
				seen[g] = true
				out = append(out, GroupInfo{ID: g, Name: g, Path: "/" + g, Members: []GroupMember{}})
			}
		}
	}
	return out, nil
}

// GetAccount reports every fake-provisioned or scanned id as an account.
func (f *FakeIdP) GetAccount(_ context.Context, userID string) (*AccountInfo, error) {
	if !strings.HasPrefix(userID, "idp:") && !f.scanned(userID) {
		return nil, ErrAccountNotFound
	}
	return &AccountInfo{Username: userID, Enabled: f.Enabled(userID)}, nil
}

func (f *FakeIdP) scanned(userID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, u := range f.Users {
		if u.ID == userID {
			return true
		}
	}
	return false
}

// UpdateMembership logs and records the membership change.
func (f *FakeIdP) UpdateMembership(_ context.Context, userID string, add, remove []string) error {
	f.logger.Info().Str("user", userID).Strs("add", add).Strs("remove", remove).Msg("fake idp membership")
	f.mu.Lock()
	f.memberships = append(f.memberships, MembershipChange{UserID: userID, Add: append([]string(nil), add...), Remove: append([]string(nil), remove...)})
	f.mu.Unlock()
	return nil
}

// Memberships returns every UpdateMembership call made so far (tests).
func (f *FakeIdP) Memberships() []MembershipChange {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]MembershipChange(nil), f.memberships...)
}

// SetFailProvision makes Provision fail with err (nil: succeed again).
func (f *FakeIdP) SetFailProvision(err error) {
	f.mu.Lock()
	f.FailProvision = err
	f.mu.Unlock()
}

// SetUsers sets what ScanUsers returns.
func (f *FakeIdP) SetUsers(users []UserInfo) {
	f.mu.Lock()
	f.Users = users
	f.mu.Unlock()
}

// SetEnabled records the enable/disable.
func (f *FakeIdP) SetEnabled(_ context.Context, userID string, enabled bool) error {
	f.logger.Info().Str("user", userID).Bool("enabled", enabled).Msg("fake idp set enabled")
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.enabled == nil {
		f.enabled = map[string]bool{}
	}
	f.enabled[userID] = enabled
	return nil
}

// Enabled reports the last SetEnabled for a user (true when never called).
func (f *FakeIdP) Enabled(userID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.enabled[userID]
	return !ok || v
}

// SendPasswordSetup records a resent set-password email.
func (f *FakeIdP) SendPasswordSetup(_ context.Context, userID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setupMails = append(f.setupMails, userID)
	return nil
}

// SetupMails lists users who got a resent set-password email.
func (f *FakeIdP) SetupMails() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.setupMails...)
}

// ScanUsers returns Users.
func (f *FakeIdP) ScanUsers(_ context.Context, fn func(UserInfo) error) error {
	f.mu.Lock()
	users := append([]UserInfo(nil), f.Users...)
	f.mu.Unlock()
	for _, u := range users {
		if err := fn(u); err != nil {
			return err
		}
	}
	return nil
}

// UpdateProfile logs the profile change.
func (f *FakeIdP) UpdateProfile(_ context.Context, userID string, p Profile) error {
	f.logger.Info().Str("user", userID).Str("username", p.Username).Str("email", p.Email).Msg("fake idp update profile")
	return nil
}

// UsernameOwner reports usernames from earlier provision calls.
func (f *FakeIdP) UsernameOwner(_ context.Context, username string) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if strings.EqualFold(c.Username, username) {
			return c.HRPersonID.String(), true, nil
		}
	}
	return "", false, nil
}

// DeleteAccount logs the deletion.
func (f *FakeIdP) DeleteAccount(_ context.Context, userID string) error {
	if !strings.HasPrefix(userID, "idp:") {
		return ErrAccountNotFound
	}
	f.logger.Info().Str("user", userID).Msg("fake idp delete account")
	return nil
}

// Calls returns a snapshot of every Provision call made so far. Intended
// for tests and the local /debug surface (not exposed in the HTTP API).
func (f *FakeIdP) Calls() []IdPUser {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]IdPUser, len(f.calls))
	copy(out, f.calls)
	return out
}
