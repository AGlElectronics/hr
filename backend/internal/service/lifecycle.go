package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AGlElectronics/hr/backend/internal/audit"
	"github.com/AGlElectronics/hr/backend/internal/db"
	"github.com/AGlElectronics/hr/backend/internal/domain"
	"github.com/AGlElectronics/hr/backend/internal/httperror"
	"github.com/AGlElectronics/hr/backend/internal/mail"
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
	mailer   mail.Sender                 // nil when email is not configured
	settings *SettingsService
	audit    *audit.Writer
	gate     *RoleGateService // nil = no training requirements
	hooks    []OffboardHook   // modules told after an offboarding
}

// SetRoleGate holds newly added roles back until their training is done.
func (s *LifecycleService) SetRoleGate(g *RoleGateService) { s.gate = g }

// OffboardHook is one module told after an offboarding (e.g. the wiki,
// which orphans the person's private space).
type OffboardHook struct {
	URL   string
	Token string
}

// ParseOffboardHooks reads OFFBOARD_HOOKS: comma-separated "url|token".
func ParseOffboardHooks(raw string) []OffboardHook {
	var out []OffboardHook
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		url, token, _ := strings.Cut(entry, "|")
		out = append(out, OffboardHook{URL: strings.TrimSpace(url), Token: strings.TrimSpace(token)})
	}
	return out
}

// SetOffboardHooks registers the modules told after an offboarding.
func (s *LifecycleService) SetOffboardHooks(hooks []OffboardHook) { s.hooks = hooks }

// notifyOffboardHooks tells each hook who left. A failed hook becomes a
// warning the admin sees; it never blocks the offboarding itself.
func (s *LifecycleService) notifyOffboardHooks(ctx context.Context, subject, username string) []string {
	var warnings []string
	for _, h := range s.hooks {
		body, _ := json.Marshal(map[string]string{"subject": subject, "username": username})
		hctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		req, err := http.NewRequestWithContext(hctx, http.MethodPost, h.URL, bytes.NewReader(body))
		if err != nil {
			cancel()
			warnings = append(warnings, "Offboarding hook "+h.URL+" is misconfigured: "+err.Error())
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		if h.Token != "" {
			req.Header.Set("Authorization", "Bearer "+h.Token)
		}
		res, err := http.DefaultClient.Do(req)
		if err == nil {
			_ = res.Body.Close()
		}
		cancel()
		if err != nil || res.StatusCode >= 300 {
			detail := ""
			if err != nil {
				detail = ": " + err.Error()
			} else {
				detail = ": HTTP " + res.Status
			}
			warnings = append(warnings, "Could not tell "+h.URL+" about the offboarding"+detail+". The module keeps their data until it hears about it.")
		}
	}
	return warnings
}

// NewLifecycleService wires a LifecycleService.
func NewLifecycleService(pool *pgxpool.Pool, people *PeopleService, drafts *DraftService, depts *DepartmentService, accounts provisioners.AccountManager, mailer mail.Sender, settings *SettingsService, aud *audit.Writer) *LifecycleService {
	return &LifecycleService{pool: pool, people: people, drafts: drafts, depts: depts, accounts: accounts, mailer: mailer, settings: settings, audit: aud}
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
	// longer exists in the IdP), deleted (removed by offboarding) or
	// unreachable (the IdP could not be asked).
	State    string `json:"state"`
	Username string `json:"username,omitempty"`
	Enabled  bool   `json:"enabled"`
	Error    string `json:"error,omitempty"`
}

// OffboardResult reports anything HR could not do while offboarding (the
// person itself is deleted).
type OffboardResult struct {
	Username string   `json:"username"`
	Warnings []string `json:"warnings"`
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
	// ShareReason includes the reason in the email to the person.
	ShareReason bool `json:"share_reason,omitempty"`
	// HROnly skips the IdP entirely (e.g. Keycloak is down); HR must then
	// delete the account by hand.
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

func (s *LifecycleService) externalID(ctx context.Context, personID uuid.UUID) (string, error) {
	return latestExternalID(ctx, s.pool, personID)
}

// latestExternalID is the IdP user id from the person's latest successful
// IdP job ("" when they never had an account).
func latestExternalID(ctx context.Context, pool *pgxpool.Pool, personID uuid.UUID) (string, error) {
	var ext string
	err := pool.QueryRow(ctx, `
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
	var target *Membership
	for i := range acc.Memberships {
		if acc.Memberships[i].DraftID == in.DraftID || (in.DraftID == uuid.Nil && len(acc.Memberships) == 1) {
			target = &acc.Memberships[i]
		}
	}
	if target == nil {
		return nil, httperror.Validation("pick which membership to change", map[string]string{"draft_id": "required"})
	}

	hadRole := map[string]bool{}
	for _, r := range target.Roles {
		hadRole[r.RoleName] = true
	}
	newRoles := make([]RolePlanInput, 0, len(in.Roles))
	added := []domain.RolePlan{}
	for _, r := range in.Roles {
		r.RoleName, r.GroupName, r.Scope = strings.TrimSpace(r.RoleName), strings.TrimSpace(r.GroupName), strings.TrimSpace(r.Scope)
		if r.RoleName == "" {
			return nil, httperror.Validation("each role requires role_name", nil)
		}
		if r.Scope == "" {
			r.Scope = "global"
		}
		newRoles = append(newRoles, r)
		if !hadRole[r.RoleName] {
			added = append(added, domain.RolePlan{RoleName: r.RoleName, GroupName: r.GroupName, Scope: r.Scope})
		}
	}

	// Roles still waiting for training, and newly added roles whose
	// training is not done, keep their group out of the account.
	held, err := s.gate.waitingRoles(ctx, personID)
	if err != nil {
		return nil, err
	}
	_, pendingNew, err := s.gate.Split(ctx, acc.ExternalID, added)
	if err != nil {
		return nil, err
	}
	for _, r := range pendingNew {
		held[r.RoleName] = true
	}
	newGroups := []string{}
	keep := membershipGroups(acc.Memberships, target.DraftID)
	if target.Department != nil {
		keep[target.Department.GroupName] = true
	}
	for _, r := range newRoles {
		if r.GroupName == "" {
			continue
		}
		keep[r.GroupName] = true
		if !held[r.RoleName] {
			newGroups = append(newGroups, r.GroupName)
		}
	}
	var dropped []string
	stillHas := map[string]bool{}
	for _, r := range newRoles {
		stillHas[r.RoleName] = true
	}
	for _, r := range target.Roles {
		if !stillHas[r.RoleName] {
			dropped = append(dropped, r.RoleName)
		}
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
	if s.gate != nil {
		if err := s.gate.RecordPending(ctx, personID, target.DraftID, pendingNew); err != nil {
			return nil, err
		}
		if err := s.gate.forgetWaiting(ctx, personID, dropped); err != nil {
			return nil, httperror.Internal(err)
		}
	}
	pendingKeys := make([]string, 0, len(pendingNew))
	for _, r := range pendingNew {
		pendingKeys = append(pendingKeys, r.RoleName)
	}
	if err := s.audit.Record(ctx, actor, "person.access.change", "person", personID, map[string]any{
		"draft_id": target.DraftID, "added_groups": newGroups, "removed_groups": remove, "pending_roles": pendingKeys,
	}); err != nil {
		return nil, err
	}
	return s.Access(ctx, personID)
}

// SyncProfile pushes name, email, login name and profile attributes to the
// person's account, if they have one. When the login name or organization
// email changed, the person gets one email listing the new values.
func (s *LifecycleService) SyncProfile(ctx context.Context, prev, p *domain.Person) (warning string, err error) {
	if s.accounts == nil {
		return "", nil
	}
	ext, err := s.externalID(ctx, p.ID)
	if err != nil || ext == "" {
		return "", err
	}
	renamed := prev != nil && p.Username != "" && !strings.EqualFold(prev.Username, p.Username)
	profile := provisioners.Profile{Email: p.Email, FirstName: p.FirstName, LastName: p.LastName, Attributes: personAttributes(p)}
	if renamed {
		profile.Username = p.Username
	}
	err = s.accounts.UpdateProfile(ctx, ext, profile)
	if errors.Is(err, provisioners.ErrAccountNotFound) {
		return "", errors.New("their account no longer exists, so only HR was updated")
	}
	if err != nil {
		return "", idpFailure("update their profile", err)
	}
	orgChanged := prev != nil && p.OrgEmail != "" && !strings.EqualFold(prev.OrgEmail, p.OrgEmail)
	if !renamed && !orgChanged {
		return "", nil
	}
	if s.mailer == nil {
		return "Their account was updated, but HR could not email them (email is not configured).", nil
	}
	settings, err := s.settings.Get(ctx)
	if err != nil {
		return "", err
	}
	if err := s.mailer.Send(ctx, AccountChangedNotice(settings.OrganizationName, p, renamed, orgChanged)); err != nil {
		return "Their account was updated, but HR could not email them: " + err.Error(), nil
	}
	return "", nil
}

// AccountChangedNotice tells the person their login name and/or
// organization email changed.
func AccountChangedNotice(org string, p *domain.Person, renamed, orgChanged bool) mail.Message {
	name := firstNonEmpty(p.Nickname, p.FirstName, p.DisplayName)
	subject := "Your account details have changed"
	if org = strings.TrimSpace(org); org != "" {
		subject = "Your " + org + " account details have changed"
	}
	var b strings.Builder
	if name != "" {
		b.WriteString("Hi " + name + ",\n\n")
	} else {
		b.WriteString("Hi,\n\n")
	}
	b.WriteString("Your account details have been updated:\n\n")
	if renamed {
		b.WriteString("  Login name: " + p.Username + "\n")
	}
	if orgChanged {
		b.WriteString("  Organization email: " + p.OrgEmail + "\n")
	}
	b.WriteString("\nYour password stays the same. If you have questions, please reply to this email.\n")
	return mail.Message{To: p.Email, Subject: subject, Body: b.String()}
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

// Offboard deletes the person's account, emails them that it was deleted,
// and deletes them from HR: profile, onboardings and earlier audit entries.
// One audit line (login name, who, when) is kept.
func (s *LifecycleService) Offboard(ctx context.Context, actor string, personID uuid.UUID, in OffboardInput) (*OffboardResult, error) {
	in.Reason = strings.TrimSpace(in.Reason)
	if in.Reason == "" {
		return nil, httperror.Validation("validation failed", map[string]string{"reason": "required"})
	}
	acc, err := s.Access(ctx, personID)
	if err != nil {
		return nil, err
	}
	warnings := []string{}
	deleted := false
	switch {
	case in.HROnly && (acc.Account.State == "linked" || acc.Account.State == "unreachable"):
		warnings = append(warnings, "Keycloak was skipped: delete their account in the Keycloak console.")
	case acc.Account.State == "unreachable":
		return nil, httperror.New(httperror.CodeUpstream, acc.Account.Error+" You can also offboard in HR only and delete the account in Keycloak by hand.")
	case acc.Account.State == "missing":
		warnings = append(warnings, "Their Keycloak account no longer exists, so there was nothing to delete.")
	case acc.Account.State == "linked" && s.accounts != nil:
		err := s.accounts.DeleteAccount(ctx, acc.ExternalID)
		if err != nil && !errors.Is(err, provisioners.ErrAccountNotFound) {
			return nil, idpFailure("delete their account", err)
		}
		deleted = err == nil
	}
	if deleted {
		if err := s.sendDeletedNotice(ctx, acc.Person, in); err != nil {
			warnings = append(warnings, "Their account was deleted, but HR could not email them: "+err.Error())
		}
	}
	username := firstNonEmpty(acc.Person.Username, acc.Person.DisplayName)
	if err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
DELETE FROM audit_events WHERE entity_id = $1
   OR entity_id IN (SELECT id FROM employment_drafts WHERE person_id = $1)`, personID); err != nil {
			return httperror.Internal(err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM employment_drafts WHERE person_id = $1`, personID); err != nil {
			return httperror.Internal(err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM people WHERE id = $1`, personID); err != nil {
			return httperror.Internal(err)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if err := s.audit.Record(ctx, actor, "person.offboard", "person", personID, map[string]any{"username": username}); err != nil {
		return nil, err
	}
	// Tell the other modules (the wiki orphans their private space). Only
	// meaningful when the person ever had an IdP account.
	if acc.ExternalID != "" {
		warnings = append(warnings, s.notifyOffboardHooks(ctx, acc.ExternalID, username)...)
	}
	return &OffboardResult{Username: username, Warnings: warnings}, nil
}

// sendDeletedNotice tells the person (at their private email) that their
// account was offboarded and deleted.
func (s *LifecycleService) sendDeletedNotice(ctx context.Context, p *domain.Person, in OffboardInput) error {
	if s.mailer == nil {
		return mail.ErrNotConfigured
	}
	if strings.TrimSpace(p.Email) == "" {
		return errors.New("they have no private email on file")
	}
	settings, err := s.settings.Get(ctx)
	if err != nil {
		return err
	}
	return s.mailer.Send(ctx, DeletedNotice(settings.OrganizationName, p, in))
}

// DeletedNotice is the "your account was deleted" email.
func DeletedNotice(org string, p *domain.Person, in OffboardInput) mail.Message {
	name := firstNonEmpty(p.Nickname, p.FirstName, p.DisplayName)
	where := "our volunteer services"
	subject := "Your volunteer account has been deleted"
	if org = strings.TrimSpace(org); org != "" {
		where = org
		subject = "Your " + org + " account has been deleted"
	}
	var b strings.Builder
	if name != "" {
		b.WriteString("Hi " + name + ",\n\n")
	} else {
		b.WriteString("Hi,\n\n")
	}
	b.WriteString("You have been offboarded from " + where + ", so your account has been deleted.\n")
	b.WriteString("You can no longer sign in with it.\n")
	if in.ShareReason && strings.TrimSpace(in.Reason) != "" {
		b.WriteString("\nReason: " + strings.TrimSpace(in.Reason) + "\n")
	}
	b.WriteString("\nIf you think this is a mistake, please reply to this email or contact the team.\n\nThank you for your help!\n")
	return mail.Message{To: p.Email, Subject: subject, Body: b.String()}
}
