// Package domain holds HR domain types and the provisioning state machine
// stub. See hr/docs/domain-model.md for the narrative view.
package domain

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// -----------------------------------------------------------------------------
// Core entities (plain Go shapes; persistence is in internal/service for now)
// -----------------------------------------------------------------------------

// Person is the stable internal identity for a human in the HR module.
// It survives across employment drafts, provisioning cycles and departures.
//
// Email is the person's private address (required): it is the IdP username
// and where account credentials are sent. OrgEmail is the organization /
// workspace account, set once provisioned.
type Person struct {
	ID               uuid.UUID         `json:"id"`
	FirstName        string            `json:"first_name"`
	LastName         string            `json:"last_name"`
	Nickname         string            `json:"nickname,omitempty"`
	DateOfBirth      string            `json:"date_of_birth,omitempty"` // YYYY-MM-DD
	IsBanned         bool              `json:"is_banned"`
	LegalName        string            `json:"legal_name"`
	DisplayName      string            `json:"display_name"`
	Email            string            `json:"email"`
	EmailMissing     bool              `json:"email_missing"`
	OrgEmail         string            `json:"org_email,omitempty"`
	Phone            string            `json:"phone,omitempty"`
	Address          string            `json:"address,omitempty"`
	SocialHandles    map[string]string `json:"social_handles"`
	EmergencyContact EmergencyContact  `json:"emergency_contact"`
	Status           PersonStatus      `json:"status"`
	Notes            string            `json:"notes,omitempty"`
	OffboardedAt     *time.Time        `json:"offboarded_at,omitempty"`
	OffboardReason   string            `json:"offboard_reason,omitempty"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
}

// EmergencyContact is who to call if something happens on shift.
type EmergencyContact struct {
	Name     string `json:"name,omitempty"`
	Phone    string `json:"phone,omitempty"`
	Email    string `json:"email,omitempty"`
	Comments string `json:"comments,omitempty"`
}

// PersonStatus is the volunteer lifecycle status.
type PersonStatus string

const (
	PersonApplicant  PersonStatus = "applicant"
	PersonOnboarding PersonStatus = "onboarding"
	PersonActive     PersonStatus = "active"
	PersonPaused     PersonStatus = "paused"
	PersonOffboarded PersonStatus = "offboarded"
)

// Valid reports whether s is a known status.
func (s PersonStatus) Valid() bool {
	switch s {
	case PersonApplicant, PersonOnboarding, PersonActive, PersonPaused, PersonOffboarded:
		return true
	}
	return false
}

// PlaceholderEmailDomain marks backfilled rows that still need a real email.
const PlaceholderEmailDomain = "@email-required.invalid"

// Department is an organizational unit volunteers join. Each one is backed by
// an IdP group (GroupName) and carries the module access new members get by
// default; HR can adjust per person during onboarding.
type Department struct {
	ID           uuid.UUID  `json:"id"`
	Slug         string     `json:"slug"`
	Name         string     `json:"name"`
	Description  string     `json:"description"`
	GroupName    string     `json:"group_name"`
	DefaultRoles []RoleRef  `json:"default_roles"`
	IdPSyncedAt  *time.Time `json:"idp_synced_at,omitempty"`
	IdPSyncError string     `json:"idp_sync_error,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// RoleRef is a module role reference ("<module>:<key>") plus its IdP group.
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
}

// RolePlan is a desired role / group assignment for a draft. On provisioning
// each plan produces (or updates) a corresponding IdP group membership.
type RolePlan struct {
	ID        uuid.UUID `json:"id"`
	DraftID   uuid.UUID `json:"draft_id"`
	RoleName  string    `json:"role_name"`
	GroupName string    `json:"group_name,omitempty"`
	Scope     string    `json:"scope,omitempty"` // e.g. "global", "dept:xyz"
	CreatedAt time.Time `json:"created_at"`
}

// ProvisioningJob tracks one attempt to push a draft to the external
// systems (IdP + workspace). Jobs are append-only; retries create new jobs.
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

// Approval records an HR decision on a draft.
type Approval struct {
	ID         uuid.UUID      `json:"id"`
	DraftID    uuid.UUID      `json:"draft_id"`
	Decision   ApprovalResult `json:"decision"`
	Reason     string         `json:"reason,omitempty"`
	ApprovedBy string         `json:"approved_by"`
	ApprovedAt time.Time      `json:"approved_at"`
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
	// DraftStatePendingApproval: provisioning succeeded, awaiting HR approval.
	DraftStatePendingApproval DraftState = "pending_approval"
	// DraftStateActive: approved and announced to the user.
	DraftStateActive DraftState = "active"
	// DraftStateRejected: approver denied; a new draft is required to retry.
	DraftStateRejected DraftState = "rejected"
)

// AllDraftStates lists every valid state (useful for validation and docs).
func AllDraftStates() []DraftState {
	return []DraftState{
		DraftStateDraft,
		DraftStateProvisioning,
		DraftStatePendingApproval,
		DraftStateActive,
		DraftStateRejected,
	}
}

// ProvisionTarget is the external system a job targets.
type ProvisionTarget string

const (
	// TargetIdP: identity provider (planned: Keycloak). The HR module is
	// *not* the login source of truth; the IdP is. See ADR-0001.
	TargetIdP ProvisionTarget = "idp"
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

// ApprovalResult is HR's final verdict on a draft.
type ApprovalResult string

const (
	ApprovalApproved ApprovalResult = "approved"
	ApprovalRejected ApprovalResult = "rejected"
)

// -----------------------------------------------------------------------------
// State machine stub
// -----------------------------------------------------------------------------

// draftTransitions encodes the legal state transitions:
//
//	draft           -> provisioning
//	provisioning    -> pending_approval | active | rejected | draft
//	pending_approval -> active | rejected
//
// provisioning -> active is the onboarding-flow "complete" path (HR reviewed
// on the overview page); provisioning -> draft lets HR retry after a
// connector failure. Rejected and active are terminal.
var draftTransitions = map[DraftState]map[DraftState]bool{
	DraftStateDraft: {
		DraftStateProvisioning: true,
	},
	DraftStateProvisioning: {
		DraftStatePendingApproval: true,
		DraftStateActive:          true,
		DraftStateRejected:        true,
		DraftStateDraft:           true,
	},
	DraftStatePendingApproval: {
		DraftStateActive:   true,
		DraftStateRejected: true,
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
