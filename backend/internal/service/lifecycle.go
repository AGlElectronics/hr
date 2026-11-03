package service

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AGlElectronics/hr/backend/internal/audit"
	"github.com/AGlElectronics/hr/backend/internal/db"
	"github.com/AGlElectronics/hr/backend/internal/domain"
	"github.com/AGlElectronics/hr/backend/internal/httperror"
	"github.com/AGlElectronics/hr/backend/internal/provisioners"
)

// LifecycleService handles people after onboarding: changing access,
// pausing, and offboarding. Every change is mirrored to the IdP.
type LifecycleService struct {
	pool     *pgxpool.Pool
	people   *PeopleService
	drafts   *DraftService
	depts    *DepartmentService
	accounts provisioners.AccountManager // nil when the IdP cannot manage accounts
	audit    *audit.Writer
}

// NewLifecycleService wires a LifecycleService.
func NewLifecycleService(pool *pgxpool.Pool, people *PeopleService, drafts *DraftService, depts *DepartmentService, accounts provisioners.AccountManager, aud *audit.Writer) *LifecycleService {
	return &LifecycleService{pool: pool, people: people, drafts: drafts, depts: depts, accounts: accounts, audit: aud}
}

// PersonAccess is what a person currently holds through HR.
type PersonAccess struct {
	Person      *domain.Person           `json:"person"`
	Memberships []Membership             `json:"memberships"`
	ExternalID  string                   `json:"external_id,omitempty"`
	Account     AccountState             `json:"account"`
	Onboardings []domain.EmploymentDraft `json:"onboardings"`
}

// AccountState describes the person's IdP login as HR last saw it.
type AccountState struct {
	// State is none (never provisioned), linked, missing (the stored id no
	// longer exists in the IdP) or unreachable (the IdP could not be asked).
	State    string `json:"state"`
	Username string `json:"username,omitempty"`
	Enabled  bool   `json:"enabled"`
	Error    string `json:"error,omitempty"`
}

// OffboardResult is the offboarded person plus anything HR could not do.
type OffboardResult struct {
	Person   *domain.Person `json:"person"`
	Warnings []string       `json:"warnings"`
}

// idpFailure turns an IdP error into a message HR can act on.
func idpFailure(action string, err error) error {
	var netErr net.Error
	var adminErr *provisioners.AdminError
	switch {
	case errors.As(err, &netErr) || strings.Contains(err.Error(), "connection refused") || strings.Contains(err.Error(), "no such host"):
		return httperror.New(httperror.CodeUpstream, "Keycloak is unreachable, so HR could not "+action+". Check that Keycloak is running and try again.").WithCause(err)
	case errors.As(err, &adminErr) && adminErr.Status == http.StatusForbidden:
		return httperror.New(httperror.CodeUpstream, "HR's Keycloak service account is not allowed to "+action+" (needs manage-users).").WithCause(err)
	case strings.Contains(err.Error(), "keycloak token"):
		return httperror.New(httperror.CodeUpstream, "HR could not sign in to Keycloak to "+action+"; check KEYCLOAK_CLIENT_ID/SECRET.").WithCause(err)
	default:
		return httperror.New(httperror.CodeUpstream, "Keycloak refused to "+action+": "+err.Error()).WithCause(err)
	}
}

func (s *LifecycleService) accountState(ctx context.Context, ext string) AccountState {
	if ext == "" {
		return AccountState{State: "none"}
	}
	if s.accounts == nil {
		return AccountState{State: "linked", Enabled: true}
	}
	info, err := s.accounts.GetAccount(ctx, ext)
	switch {
	case errors.Is(err, provisioners.ErrAccountNotFound):
		return AccountState{State: "missing"}
	case err != nil:
		return AccountState{State: "unreachable", Error: idpFailure("look up the account", err).Error()}
	default:
		return AccountState{State: "linked", Username: info.Username, Enabled: info.Enabled}
	}
}

// Membership is one completed onboarding: a department and its roles.
type Membership struct {
	DraftID    uuid.UUID          `json:"draft_id"`
	Department *domain.Department `json:"department,omitempty"`
	Roles      []domain.RolePlan  `json:"roles"`
}

// ChangeAccessInput replaces the roles of one membership.
type ChangeAccessInput struct {
	DraftID uuid.UUID       `json:"draft_id"`
	Roles   []RolePlanInput `json:"roles"`
}

// OffboardInput explains why someone leaves.
type OffboardInput struct {
	Reason string `json:"reason"`
	// KeepAccount leaves the IdP login enabled (groups are still removed).
	KeepAccount bool `json:"keep_account,omitempty"`
	// HROnly skips the IdP entirely (e.g. Keycloak is down); HR must then
	// clean up the account by hand.
	HROnly bool `json:"hr_only,omitempty"`
}

// StatusInput pauses or reactivates a person.
type StatusInput struct {
	Status string `json:"status"`
}

func (s *LifecycleService) personDrafts(ctx context.Context, personID uuid.UUID) ([]domain.EmploymentDraft, error) {
	rows, err := s.pool.Query(ctx, `SELECT id FROM employment_drafts WHERE person_id = $1 ORDER BY updated_at DESC`, personID)
	if err != nil {
		return nil, httperror.Internal(err)
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, httperror.Internal(err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	out := make([]domain.EmploymentDraft, 0, len(ids))
	for _, id := range ids {
		d, err := s.drafts.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, nil
}

// externalID is the IdP user id from the person's latest successful IdP job.
func (s *LifecycleService) externalID(ctx context.Context, personID uuid.UUID) (string, error) {
	var ext string
	err := s.pool.QueryRow(ctx, `
SELECT j.external_id FROM provisioning_jobs j
  JOIN employment_drafts d ON d.id = j.draft_id
 WHERE d.person_id = $1 AND j.target = 'idp' AND j.status = 'succeeded' AND j.external_id IS NOT NULL
 ORDER BY j.started_at DESC LIMIT 1`, personID).Scan(&ext)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", httperror.Internal(err)
	}
	return ext, nil
}

// Access returns the person, their completed memberships and onboarding history.
func (s *LifecycleService) Access(ctx context.Context, personID uuid.UUID) (*PersonAccess, error) {
	person, err := s.people.Get(ctx, personID)
	if err != nil {
		return nil, err
	}
	drafts, err := s.personDrafts(ctx, personID)
	if err != nil {
		return nil, err
	}
	ext, err := s.externalID(ctx, personID)
	if err != nil {
		return nil, err
	}
	out := &PersonAccess{Person: person, ExternalID: ext, Account: s.accountState(ctx, ext), Onboardings: drafts, Memberships: []Membership{}}
	if person.Status == domain.PersonOffboarded {
		return out, nil
	}
	for _, d := range drafts {
		if d.State != domain.DraftStateActive {
			continue
		}
		m := Membership{DraftID: d.ID, Roles: d.Roles}
		if m.Roles == nil {
			m.Roles = []domain.RolePlan{}
		}
		if d.DepartmentID != nil {
			if m.Department, err = s.depts.Get(ctx, *d.DepartmentID); err != nil {
				return nil, err
			}
		}
		out.Memberships = append(out.Memberships, m)
	}
	return out, nil
}

func membershipGroups(ms []Membership, skip uuid.UUID) map[string]bool {
	set := map[string]bool{}
	for _, m := range ms {
		if m.DraftID == skip {
			continue
		}
		if m.Department != nil {
			set[m.Department.GroupName] = true
		}
		for _, r := range m.Roles {
			if r.GroupName != "" {
				set[r.GroupName] = true
			}
		}
	}
	return set
}

// ChangeAccess replaces the roles of one completed membership and syncs the
// IdP groups (adds new ones, removes ones no other membership still needs).
func (s *LifecycleService) ChangeAccess(ctx context.Context, actor string, personID uuid.UUID, in ChangeAccessInput) (*PersonAccess, error) {
	acc, err := s.Access(ctx, personID)
	if err != nil {
		return nil, err
	}
	if acc.Person.Status == domain.PersonOffboarded {
		return nil, httperror.Conflict("person is offboarded; start a new onboarding instead")
	}
	var target *Membership
	for i := range acc.Memberships {
		if acc.Memberships[i].DraftID == in.DraftID || (in.DraftID == uuid.Nil && len(acc.Memberships) == 1) {
			target = &acc.Memberships[i]
		}
	}
	if target == nil {
		return nil, httperror.Validation("pick which membership to change", map[string]string{"draft_id": "required"})
	}

	newRoles := make([]RolePlanInput, 0, len(in.Roles))
	newGroups := []string{}
	for _, r := range in.Roles {
		r.RoleName, r.GroupName, r.Scope = strings.TrimSpace(r.RoleName), strings.TrimSpace(r.GroupName), strings.TrimSpace(r.Scope)
		if r.RoleName == "" {
			return nil, httperror.Validation("each role requires role_name", nil)
		}
		if r.Scope == "" {
			r.Scope = "global"
		}
		newRoles = append(newRoles, r)
		if r.GroupName != "" {
			newGroups = append(newGroups, r.GroupName)
		}
	}
	keep := membershipGroups(acc.Memberships, target.DraftID)
	if target.Department != nil {
		keep[target.Department.GroupName] = true
	}
	for _, g := range newGroups {
		keep[g] = true
	}
	var remove []string
	for _, r := range target.Roles {
		if r.GroupName != "" && !keep[r.GroupName] {
			remove = append(remove, r.GroupName)
		}
	}

	switch acc.Account.State {
	case "missing":
		return nil, httperror.Conflict("This person's Keycloak account no longer exists, so access cannot be synced. Offboard them and onboard again to create a new account.")
	case "unreachable":
		return nil, httperror.New(httperror.CodeUpstream, acc.Account.Error)
	case "linked":
		if s.accounts != nil {
			if err := s.accounts.UpdateMembership(ctx, acc.ExternalID, newGroups, remove); err != nil {
				return nil, idpFailure("change their groups", err)
			}
		}
	}
	if err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM role_plans WHERE draft_id = $1`, target.DraftID); err != nil {
			return httperror.Internal(err)
		}
		for _, r := range newRoles {
			if _, err := tx.Exec(ctx, `
INSERT INTO role_plans (id, draft_id, role_name, group_name, scope) VALUES ($1, $2, $3, NULLIF($4, ''), $5)`,
				uuid.New(), target.DraftID, r.RoleName, r.GroupName, r.Scope); err != nil {
				return httperror.Internal(err)
			}
		}
		_, err := tx.Exec(ctx, `UPDATE employment_drafts SET updated_at = now() WHERE id = $1`, target.DraftID)
		return err
	}); err != nil {
		return nil, err
	}
	if err := s.audit.Record(ctx, actor, "person.access.change", "person", personID, map[string]any{
		"draft_id": target.DraftID, "added_groups": newGroups, "removed_groups": remove,
	}); err != nil {
		return nil, err
	}
	return s.Access(ctx, personID)
}

// SyncProfile pushes name and email changes to the person's IdP account,
// if they have one.
func (s *LifecycleService) SyncProfile(ctx context.Context, p *domain.Person) error {
	if s.accounts == nil || p.EmailMissing || p.Status == domain.PersonOffboarded {
		return nil
	}
	ext, err := s.externalID(ctx, p.ID)
	if err != nil || ext == "" {
		return err
	}
	err = s.accounts.UpdateProfile(ctx, ext, p.Email, p.FirstName, p.LastName)
	if errors.Is(err, provisioners.ErrAccountNotFound) {
		return errors.New("their Keycloak account no longer exists, so only HR was updated")
	}
	if err != nil {
		return idpFailure("update their name and email", err)
	}
	return nil
}

// SetStatus pauses an active person or reactivates a paused one. Pausing
// keeps their access; offboard to remove it.
func (s *LifecycleService) SetStatus(ctx context.Context, actor string, personID uuid.UUID, in StatusInput) (*domain.Person, error) {
	p, err := s.people.Get(ctx, personID)
	if err != nil {
		return nil, err
	}
	to := domain.PersonStatus(strings.TrimSpace(in.Status))
	switch {
	case to == domain.PersonPaused && p.Status == domain.PersonActive:
	case to == domain.PersonActive && p.Status == domain.PersonPaused:
	default:
		return nil, httperror.Conflict("only active ↔ paused can be changed here (status is " + string(p.Status) + ")")
	}
	if err := s.people.SetStatus(ctx, personID, to); err != nil {
		return nil, err
	}
	if err := s.audit.Record(ctx, actor, "person.status."+string(to), "person", personID, nil); err != nil {
		return nil, err
	}
	return s.people.Get(ctx, personID)
}

// Offboard removes everything HR granted: the person leaves all department
// and role groups, their login is disabled (unless KeepAccount), and their
// status becomes offboarded. Their profile and history are kept.
func (s *LifecycleService) Offboard(ctx context.Context, actor string, personID uuid.UUID, in OffboardInput) (*OffboardResult, error) {
	in.Reason = strings.TrimSpace(in.Reason)
	if in.Reason == "" {
		return nil, httperror.Validation("validation failed", map[string]string{"reason": "required"})
	}
	acc, err := s.Access(ctx, personID)
	if err != nil {
		return nil, err
	}
	if acc.Person.Status == domain.PersonOffboarded {
		return nil, httperror.Conflict("person is already offboarded")
	}
	groups := []string{}
	for g := range membershipGroups(acc.Memberships, uuid.Nil) {
		groups = append(groups, g)
	}
	warnings := []string{}
	disabled := false
	switch {
	case in.HROnly && (acc.Account.State == "linked" || acc.Account.State == "unreachable"):
		warnings = append(warnings, "Keycloak was skipped: remove their groups and disable their login in the Keycloak console.")
	case acc.Account.State == "unreachable":
		return nil, httperror.New(httperror.CodeUpstream, acc.Account.Error+" You can also offboard in HR only and clean up Keycloak by hand.")
	case acc.Account.State == "missing":
		warnings = append(warnings, "Their Keycloak account no longer exists, so there was nothing to disable.")
	case acc.Account.State == "linked" && s.accounts != nil:
		if err := s.accounts.UpdateMembership(ctx, acc.ExternalID, nil, groups); err != nil && !errors.Is(err, provisioners.ErrAccountNotFound) {
			return nil, idpFailure("remove their groups", err)
		}
		if !in.KeepAccount {
			if err := s.accounts.SetEnabled(ctx, acc.ExternalID, false); err != nil && !errors.Is(err, provisioners.ErrAccountNotFound) {
				return nil, idpFailure("disable their login (groups were already removed)", err)
			}
			disabled = true
		}
	}
	if _, err := s.pool.Exec(ctx, `
UPDATE people SET status = 'offboarded', offboarded_at = now(), offboard_reason = $2, updated_at = now() WHERE id = $1`,
		personID, in.Reason); err != nil {
		return nil, httperror.Internal(err)
	}
	if err := s.audit.Record(ctx, actor, "person.offboard", "person", personID, map[string]any{
		"reason": in.Reason, "removed_groups": groups, "account_disabled": disabled,
		"account_state": acc.Account.State, "hr_only": in.HROnly, "warnings": warnings,
	}); err != nil {
		return nil, err
	}
	p, err := s.people.Get(ctx, personID)
	if err != nil {
		return nil, err
	}
	return &OffboardResult{Person: p, Warnings: warnings}, nil
}
