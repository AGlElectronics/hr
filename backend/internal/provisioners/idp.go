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
	// TemporaryPassword is set when a brand-new IdP user was created and
	// needs a one-time password (MVP Keycloak path). Empty on updates / fakes.
	TemporaryPassword string
	// Created is true when the IdP user did not exist before this call.
	Created bool
	// PasswordSetupEmailSent is true when the IdP emailed a set-password link.
	PasswordSetupEmailSent bool
	// PasswordSetupError explains why the email could not be sent; in that
	// case TemporaryPassword is set as a fallback.
	PasswordSetupError string
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

// Profile is what HR keeps in sync on an existing account.
type Profile struct {
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

	mu    sync.Mutex
	calls []IdPUser
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
	f.mu.Unlock()

	// Deterministic external id: "idp:<uuid>" so tests can assert exact
	// values without pre-seeding anything.
	ext := "idp:" + u.HRPersonID.String()
	f.logger.Info().
		Str("hr_person_id", u.HRPersonID.String()).
		Str("username", u.Username).
		Strs("groups", u.Groups).
		Str("external_id", ext).
		Time("at", time.Now().UTC()).
		Msg("fake idp provision")

	// Copy groups so the caller cannot mutate FakeIdP's stored call.
	applied := append([]string(nil), u.Groups...)
	return IdPResult{ExternalID: ext, GroupsApplied: applied, Created: true, PasswordSetupEmailSent: u.SendPasswordSetup}, nil
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

// GetAccount reports every fake-provisioned id as an enabled account.
func (f *FakeIdP) GetAccount(_ context.Context, userID string) (*AccountInfo, error) {
	if !strings.HasPrefix(userID, "idp:") {
		return nil, ErrAccountNotFound
	}
	return &AccountInfo{Username: userID, Enabled: true}, nil
}

// UpdateMembership logs the membership change.
func (f *FakeIdP) UpdateMembership(_ context.Context, userID string, add, remove []string) error {
	f.logger.Info().Str("user", userID).Strs("add", add).Strs("remove", remove).Msg("fake idp membership")
	return nil
}

// SetEnabled logs the enable/disable.
func (f *FakeIdP) SetEnabled(_ context.Context, userID string, enabled bool) error {
	f.logger.Info().Str("user", userID).Bool("enabled", enabled).Msg("fake idp set enabled")
	return nil
}

// UpdateProfile logs the profile change.
func (f *FakeIdP) UpdateProfile(_ context.Context, userID string, p Profile) error {
	f.logger.Info().Str("user", userID).Str("email", p.Email).Msg("fake idp update profile")
	return nil
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
