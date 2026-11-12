// Package domain holds HR domain types and the provisioning state machine
// stub. See hr/docs/domain-model.md for the narrative view.
package domain

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// -----------------------------------------------------------------------------
// Core entities (plain Go shapes; persistence is in internal/service for now)
// -----------------------------------------------------------------------------

// Person is the stable internal identity for a human in the HR module.
// It survives across employment drafts, provisioning cycles and departures.
//
// Email is the person's private address (required): account emails
// (set-password links, notices) go there. Username is the login name built
// from the username template (by default the nickname). OrgEmail is the
// organization / workspace account.
type Person struct {
	ID               uuid.UUID         `json:"id"`
	FirstName        string            `json:"first_name"`
	LastName         string            `json:"last_name"`
	Nickname         string            `json:"nickname,omitempty"`
	Username         string            `json:"username,omitempty"`
	DateOfBirth      string            `json:"date_of_birth,omitempty"` // YYYY-MM-DD
	IsBanned         bool              `json:"is_banned"`
	LegalName        string            `json:"legal_name"`
	DisplayName      string            `json:"display_name"`
	Email            string            `json:"email"`
	OrgEmail         string            `json:"org_email,omitempty"`
	Phone            string            `json:"phone,omitempty"`
	Address          Address           `json:"address"`
	SocialHandles    map[string]string `json:"social_handles"`
	EmergencyContact EmergencyContact  `json:"emergency_contact"`
	Status           PersonStatus      `json:"status"`
	Notes            string            `json:"notes,omitempty"`
	// IDPUserID is the account's id in the identity provider (Keycloak user
	// id); IDPSubject is the OIDC `sub` other modules know the person by.
	IDPUserID  string     `json:"idp_user_id,omitempty"`
	IDPSubject string     `json:"idp_subject,omitempty"`
	PausedAt   *time.Time `json:"paused_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// PersonSummary is the reduced shape of the people list: enough to find
// someone, without date of birth, phone, address or emergency contact.
type PersonSummary struct {
	ID          uuid.UUID    `json:"id"`
	DisplayName string       `json:"display_name"`
	LegalName   string       `json:"legal_name"`
	Nickname    string       `json:"nickname,omitempty"`
	Username    string       `json:"username,omitempty"`
	Email       string       `json:"email"`
	OrgEmail    string       `json:"org_email,omitempty"`
	Status      PersonStatus `json:"status"`
	IsBanned    bool         `json:"is_banned"`
	UpdatedAt   time.Time    `json:"updated_at"`
}

// Address is a postal address. Country is an ISO 3166-1 alpha-2 code.
type Address struct {
	Street     string `json:"street"`
	PostalCode string `json:"postal_code"`
	City       string `json:"city"`
	Country    string `json:"country"`
}

// Line is the address on one line ("Street 1, 1010 Vienna, AT").
func (a Address) Line() string {
	parts := []string{}
	for _, p := range []string{a.Street, joinNonEmpty(a.PostalCode, a.City), a.Country} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, ", ")
}

func joinNonEmpty(a, b string) string {
	return strings.TrimSpace(a + " " + b)
}

// EmergencyContact is who to call if something happens on shift.
type EmergencyContact struct {
	Name     string `json:"name,omitempty"`
	Phone    string `json:"phone,omitempty"`
	Email    string `json:"email,omitempty"`
	Comments string `json:"comments,omitempty"`
}

// PersonStatus is the volunteer lifecycle status. Offboarding deletes the
// person, so there is no "offboarded" status.
type PersonStatus string

const (
	PersonApplicant  PersonStatus = "applicant"
	PersonOnboarding PersonStatus = "onboarding"
	PersonActive     PersonStatus = "active"
	PersonPaused     PersonStatus = "paused"
)

// Valid reports whether s is a known status.
func (s PersonStatus) Valid() bool {
	switch s {
	case PersonApplicant, PersonOnboarding, PersonActive, PersonPaused:
		return true
	}
	return false
}

// Department is an organizational unit volunteers join. Each one is backed by
// an IDP group (GroupName) and carries the module access new members get by
// default; HR can adjust per person during onboarding.
type Department struct {
	ID           uuid.UUID  `json:"id"`
	Slug         string     `json:"slug"`
	Name         string     `json:"name"`
	Description  string     `json:"description"`
	GroupName    string     `json:"group_name"`
	DefaultRoles []RoleRef  `json:"default_roles"`
	IDPSyncedAt  *time.Time `json:"idp_synced_at,omitempty"`
	IDPSyncError string     `json:"idp_sync_error,omitempty"`
	MemberCount  int        `json:"member_count"`
	// LeadGroup is the IDP group HR gives the department's leads
	// ("dept-<slug>-lead"); other modules may use it too.
	LeadGroup string    `json:"lead_group"`
	Leads     []LeadRef `json:"leads"`
	// DefaultBundles are access groups every new member gets by default.
	DefaultBundles []uuid.UUID `json:"default_bundles"`
	// ArchivedAt hides the department from new onboardings; existing
	// memberships stay.
	ArchivedAt *time.Time `json:"archived_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// LeadRef is one person on a department's lead list.
type LeadRef struct {
	PersonID    uuid.UUID `json:"person_id"`
	DisplayName string    `json:"display_name"`
}

// LeadGroupFor is the IDP group of a department's leads.
func LeadGroupFor(deptGroup string) string { return deptGroup + "-lead" }

// RoleRef is a module role reference ("<module>:<key>") plus its IDP group.
type RoleRef struct {
	RoleName  string `json:"role_name"`
	GroupName string `json:"group_name,omitempty"`
	Scope     string `json:"scope,omitempty"`
}

// EmploymentDraft is the HR-authored proposed employment record. It carries
// the DraftState state machine (see Transition below).
type EmploymentDraft struct {
	ID           uuid.UUID  `json:"id"`
	PersonID     uuid.UUID  `json:"person_id"`
	DepartmentID *uuid.UUID `json:"department_id,omitempty"`
	Title        string     `json:"title"`
	StartDate    *time.Time `json:"start_date,omitempty"`
	EndDate      *time.Time `json:"end_date,omitempty"`
	State        DraftState `json:"state"`
	Notes        string     `json:"notes,omitempty"`
	CreatedBy    string     `json:"created_by"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	Roles        []RolePlan `json:"roles,omitempty"`
	// BundleIDs are the access groups the draft holds.
	BundleIDs []uuid.UUID `json:"bundle_ids,omitempty"`
	// PersonName and DepartmentName are filled in lists.
	PersonName     string `json:"person_name,omitempty"`
	DepartmentName string `json:"department_name,omitempty"`
}

// RolePlan is a desired role / group assignment for a draft. On provisioning
// each plan produces (or updates) a corresponding IDP group membership.
type RolePlan struct {
	ID        uuid.UUID `json:"id"`
	DraftID   uuid.UUID `json:"draft_id"`
	RoleName  string    `json:"role_name"`
	GroupName string    `json:"group_name,omitempty"`
	Scope     string    `json:"scope,omitempty"` // e.g. "global", "dept:xyz"
	CreatedAt time.Time `json:"created_at"`
	// BundleID and BundleName say which access group the role came from
	// (empty: assigned individually).
	BundleID   *uuid.UUID `json:"bundle_id,omitempty"`
	BundleName string     `json:"bundle_name,omitempty"`
	// Retired is true when the module no longer publishes the role; HR
	// keeps it until someone removes it.
	Retired bool `json:"retired,omitempty"`
}

// ProvisioningJob tracks one attempt to push a draft to the external
// systems (IDP + workspace). Jobs are append-only; retries create new jobs.
type ProvisioningJob struct {
	ID         uuid.UUID       `json:"id"`
	DraftID    uuid.UUID       `json:"draft_id"`
	Target     ProvisionTarget `json:"target"`
	Status     JobStatus       `json:"status"`
	Attempt    int             `json:"attempt"`
	Message    string          `json:"message,omitempty"`
	StartedAt  time.Time       `json:"started_at"`
	FinishedAt *time.Time      `json:"finished_at,omitempty"`
	ExternalID string          `json:"external_id,omitempty"`
}

// AuditEvent is the append-only HR audit record. Every state-changing write
// should produce one.
type AuditEvent struct {
	ID         uuid.UUID `json:"id"`
	ActorName  string    `json:"actor_name"`
	Action     string    `json:"action"`
	EntityType string    `json:"entity_type"`
	EntityID   uuid.UUID `json:"entity_id"`
	Metadata   any       `json:"metadata,omitempty"`
	OccurredAt time.Time `json:"occurred_at"`
}

// -----------------------------------------------------------------------------
// Enumerations
// -----------------------------------------------------------------------------

// DraftState is the lifecycle of an EmploymentDraft.
type DraftState string

const (
	// DraftStateDraft: HR is still editing. No external systems touched.
	DraftStateDraft DraftState = "draft"
	// DraftStateProvisioning: a provisioning job is in flight.
	DraftStateProvisioning DraftState = "provisioning"
	// DraftStateActive: completed; the account exists and the person was emailed.
	DraftStateActive DraftState = "active"
	// DraftStateRejected: abandoned; a new draft is required to retry.
	DraftStateRejected DraftState = "rejected"
)

// AllDraftStates lists every valid state (useful for validation and docs).
func AllDraftStates() []DraftState {
	return []DraftState{
		DraftStateDraft,
		DraftStateProvisioning,
		DraftStateActive,
		DraftStateRejected,
	}
}

// ProvisionTarget is the external system a job targets.
type ProvisionTarget string

const (
	// TargetIDP: identity provider (planned: Keycloak). The HR module is
	// *not* the login source of truth; the IDP is. See ADR-0001.
	TargetIDP ProvisionTarget = "idp"
	// TargetWorkspace: productivity / collaboration (planned: Google).
	TargetWorkspace ProvisionTarget = "workspace"
)

// JobStatus is the lifecycle of a ProvisioningJob.
type JobStatus string

const (
	JobStatusPending   JobStatus = "pending"
	JobStatusRunning   JobStatus = "running"
	JobStatusSucceeded JobStatus = "succeeded"
	JobStatusFailed    JobStatus = "failed"
)

// -----------------------------------------------------------------------------
// State machine stub
// -----------------------------------------------------------------------------

// draftTransitions encodes the legal state transitions:
//
//	draft        -> provisioning | rejected
//	provisioning -> active | rejected | draft
//
// provisioning -> active is the onboarding "complete" path (HR reviewed on
// the overview page); provisioning -> draft lets HR retry after a connector
// failure; draft -> rejected cancels an onboarding. Rejected and active are
// terminal.
var draftTransitions = map[DraftState]map[DraftState]bool{
	DraftStateDraft: {
		DraftStateProvisioning: true,
		DraftStateRejected:     true,
	},
	DraftStateProvisioning: {
		DraftStateActive:   true,
		DraftStateRejected: true,
		DraftStateDraft:    true,
	},
	DraftStateActive:   {},
	DraftStateRejected: {},
}

// CanTransition reports whether draft state `from` may legally transition to
// `to`. Unknown states always return false.
func CanTransition(from, to DraftState) bool {
	allowed, ok := draftTransitions[from]
	if !ok {
		return false
	}
	return allowed[to]
}

// Transition returns `to` if the move is legal, else a descriptive error.
// Call this from service code before persisting a state change so illegal
// transitions become validation errors rather than silent data corruption.
func Transition(from, to DraftState) (DraftState, error) {
	if !CanTransition(from, to) {
		return from, fmt.Errorf("illegal draft transition %q -> %q", from, to)
	}
	return to, nil
}

// IsTerminal reports whether a draft state admits no further transitions.
func (s DraftState) IsTerminal() bool {
	outgoing, ok := draftTransitions[s]
	if !ok {
		return true
	}
	return len(outgoing) == 0
}
