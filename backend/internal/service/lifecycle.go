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
	// bundles and validator plan access against access groups and catalogs
	// (nil: roles are taken as given).
	bundles   *BundleService
	validator *AccessValidator
	// linkOnly: IDP_PROVISIONER_MODE=link (never disable or delete accounts).
	// off: IDP_PROVISIONER_MODE=off (no accounts at all).
	linkOnly, off bool
	// workspace suspends Google Workspace accounts on offboarding (nil: off).
	workspace *WorkspaceService
}

// SetMode tells the service the provisioner mode (manage, link or off).
func (s *LifecycleService) SetMode(mode string) {
	s.linkOnly, s.off = mode == "link", mode == "off"
}

// SetWorkspace suspends Workspace accounts on offboarding.
func (s *LifecycleService) SetWorkspace(w *WorkspaceService) { s.workspace = w }

// SetAccessPlanning checks access changes against the catalogs and expands
// access groups.
func (s *LifecycleService) SetAccessPlanning(b *BundleService, v *AccessValidator) {
	s.bundles, s.validator = b, v
}

func (s *LifecycleService) planAccess(ctx context.Context, roles []RolePlanInput, bundleIDs []uuid.UUID, keep []domain.RolePlan, trusted bool) ([]RolePlanInput, []uuid.UUID, error) {
	if s.bundles != nil {
		return s.bundles.Plan(ctx, roles, bundleIDs, keep, trusted)
	}
	out := make([]RolePlanInput, 0, len(roles))
	for _, r := range roles {
		r.RoleName, r.GroupName, r.Scope = strings.TrimSpace(r.RoleName), strings.TrimSpace(r.GroupName), strings.TrimSpace(r.Scope)
		if r.RoleName == "" {
			return nil, nil, httperror.Validation("each role requires role_name", nil)
		}
		if r.Scope == "" {
			r.Scope = "global"
		}
		out = append(out, r)
	}
	return out, uniqueIDs(bundleIDs), nil
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
func (s *LifecycleService) notifyOffboardHooks(ctx context.Context, subject, idpUserID, username string) []string {
	var warnings []string
	for _, h := range s.hooks {
		body, _ := json.Marshal(map[string]string{"subject": subject, "idp_user_id": idpUserID, "username": username})
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
	if s.off {
		return AccountState{State: "off"}
	}
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
	BundleIDs  []uuid.UUID        `json:"bundle_ids"`
}

// ChangeAccessInput replaces the roles of one membership. Roles are the
// individually picked roles; BundleIDs (when sent) replace the membership's
// access groups, otherwise they stay.
type ChangeAccessInput struct {
	DraftID   uuid.UUID       `json:"draft_id"`
	Roles     []RolePlanInput `json:"roles"`
	BundleIDs *[]uuid.UUID    `json:"bundle_ids,omitempty"`
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

// latestExternalID is the person's IdP user id ("" when they have no linked
// account).
func latestExternalID(ctx context.Context, pool *pgxpool.Pool, personID uuid.UUID) (string, error) {
	var ext string
	err := pool.QueryRow(ctx, `SELECT COALESCE(idp_user_id, '') FROM people WHERE id = $1`, personID).Scan(&ext)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", httperror.Internal(err)
	}
	return ext, nil
}

// linkAccount stores the IdP user id (and subject, if not known yet) on the
// person.
func linkAccount(ctx context.Context, pool *pgxpool.Pool, personID uuid.UUID, userID, subject string) error {
	if subject == "" {
		subject = userID
	}
	_, err := pool.Exec(ctx, `
UPDATE people SET idp_user_id = $2, idp_subject = CASE WHEN idp_user_id IS DISTINCT FROM $2 THEN $3 ELSE COALESCE(idp_subject, $3) END,
       updated_at = now()
 WHERE id = $1`, personID, userID, subject)
	if err != nil {
		return httperror.Internal(err)
	}
	return nil
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
		m := Membership{DraftID: d.ID, Roles: d.Roles, BundleIDs: d.BundleIDs}
		if m.Roles == nil {
			m.Roles = []domain.RolePlan{}
		}
		s.validator.Annotate(ctx, m.Roles)
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
	bundleIDs := target.BundleIDs
	if in.BundleIDs != nil {
		bundleIDs = *in.BundleIDs
	}
	return s.applyAccess(ctx, actor, acc, target, in.Roles, bundleIDs, false, "person.access.change")
}

// ResyncBundle re-applies the access groups of one membership or onboarding
// after an admin edited a bundle (background job "bundle.sync").
func (s *LifecycleService) ResyncBundle(ctx context.Context, actor string, draftID, bundleID uuid.UUID) error {
	d, err := s.drafts.Get(ctx, draftID)
	if err != nil {
		return err
	}
	switch d.State {
	case domain.DraftStateDraft:
		_, err := s.drafts.AssignDefaults(ctx, actor, draftID, AssignRolesInput{Roles: individualRoles(d.Roles), BundleIDs: d.BundleIDs})
		return err
	case domain.DraftStateActive:
		acc, err := s.Access(ctx, d.PersonID)
		if err != nil {
			return err
		}
		for i := range acc.Memberships {
			if acc.Memberships[i].DraftID == draftID {
				_, err := s.applyAccess(ctx, actor, acc, &acc.Memberships[i], individualRoles(d.Roles), d.BundleIDs, true,
					"bundle.sync")
				return err
			}
		}
	}
	return nil
}

// applyAccess replaces a membership's roles and access groups and syncs the
// IdP groups (adds new ones, removes ones no other membership still needs).
// trusted skips the "assign admin roles" check (bundle syncs).
func (s *LifecycleService) applyAccess(ctx context.Context, actor string, acc *PersonAccess, target *Membership, roles []RolePlanInput, bundleIDs []uuid.UUID, trusted bool, action string) (*PersonAccess, error) {
	personID := acc.Person.ID
	newRoles, bundleIDs, err := s.planAccess(ctx, roles, bundleIDs, target.Roles, trusted)
	if err != nil {
		return nil, err
	}
	hadRole := map[string]bool{}
	for _, r := range target.Roles {
		hadRole[r.RoleName] = true
	}
	added := []domain.RolePlan{}
	for _, r := range newRoles {
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
		return writePlans(ctx, tx, target.DraftID, newRoles, bundleIDs)
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
	if err := s.audit.Record(ctx, actor, action, "person", personID, map[string]any{
		"draft_id": target.DraftID, "added_groups": newGroups, "removed_groups": remove, "pending_roles": pendingKeys,
		"bundles": bundleIDs,
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

// StatusResult is the person after a status or ban change, plus anything HR
// could not do in the IdP.
type StatusResult struct {
	*domain.Person
	Warnings []string `json:"warnings"`
}

// setLogin enables or disables the person's account. Link mode never
// disables accounts in the convention's realm.
func (s *LifecycleService) setLogin(ctx context.Context, p *domain.Person, enabled bool) []string {
	if s.accounts == nil || p.IdPUserID == "" {
		return []string{}
	}
	if s.linkOnly {
		return []string{"This installation links to the convention's own accounts (link mode), so HR did not disable or enable the account."}
	}
	err := s.accounts.SetEnabled(ctx, p.IdPUserID, enabled)
	switch {
	case errors.Is(err, provisioners.ErrAccountNotFound):
		return []string{"Their account no longer exists in the identity provider."}
	case err != nil:
		verb := "disable"
		if enabled {
			verb = "enable"
		}
		return []string{idpFailure(verb+" their account", err).Error() + " Change it in the identity provider by hand."}
	}
	return []string{}
}

// SetStatus pauses an active person or reactivates a paused one. Pausing
// disables their account (they cannot sign in) and keeps their access for
// when they come back; reactivating enables it again unless they are banned.
func (s *LifecycleService) SetStatus(ctx context.Context, actor string, personID uuid.UUID, in StatusInput) (*StatusResult, error) {
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
	warnings := s.setLogin(ctx, p, to == domain.PersonActive && !p.IsBanned)
	if err := s.people.SetStatus(ctx, personID, to); err != nil {
		return nil, err
	}
	if err := s.audit.Record(ctx, actor, "person.status."+string(to), "person", personID, map[string]any{"warnings": warnings}); err != nil {
		return nil, err
	}
	p, err = s.people.Get(ctx, personID)
	if err != nil {
		return nil, err
	}
	return &StatusResult{Person: p, Warnings: warnings}, nil
}

// LinkAccount links an existing IdP account to a person (review queue
// merge): HR writes hrPersonId and the groups of the person's memberships.
func (s *LifecycleService) LinkAccount(ctx context.Context, actor string, personID uuid.UUID, userID, subject string) ([]string, error) {
	if err := linkAccount(ctx, s.pool, personID, userID, subject); err != nil {
		return nil, err
	}
	warnings := []string{}
	if s.accounts == nil {
		return warnings, nil
	}
	acc, err := s.Access(ctx, personID)
	if err != nil {
		return nil, err
	}
	held, err := s.gate.waitingRoles(ctx, personID)
	if err != nil {
		return nil, err
	}
	add := []string{}
	for _, m := range acc.Memberships {
		if m.Department != nil {
			add = append(add, m.Department.GroupName)
		}
		for _, r := range m.Roles {
			if r.GroupName != "" && !held[r.RoleName] {
				add = append(add, r.GroupName)
			}
		}
	}
	if lead, err := s.depts.LeadGroupsFor(ctx, personID); err == nil {
		add = append(add, lead...)
	}
	if len(add) > 0 {
		if err := s.accounts.UpdateMembership(ctx, userID, add, nil); err != nil {
			warnings = append(warnings, idpFailure("add their groups", err).Error())
		}
	}
	attrs := personAttributes(acc.Person)
	attrs[provisioners.AttrHRPersonID] = personID.String()
	if err := s.accounts.UpdateProfile(ctx, userID, provisioners.Profile{Email: acc.Person.Email, FirstName: acc.Person.FirstName,
		LastName: acc.Person.LastName, Attributes: attrs}); err != nil {
		warnings = append(warnings, idpFailure("write their HR attributes", err).Error())
	}
	return warnings, nil
}

// BanInput bans or unbans a person.
type BanInput struct {
	Banned bool   `json:"banned"`
	Reason string `json:"reason"`
}

// SetBan bans a person (their account is disabled and they cannot be
// onboarded again) or lifts the ban (an active person's account is enabled
// again).
func (s *LifecycleService) SetBan(ctx context.Context, actor string, personID uuid.UUID, in BanInput) (*StatusResult, error) {
	in.Reason = strings.TrimSpace(in.Reason)
	if in.Banned && in.Reason == "" {
		return nil, httperror.Validation("validation failed", map[string]string{"reason": "required"})
	}
	p, err := s.people.Get(ctx, personID)
	if err != nil {
		return nil, err
	}
	if p.IsBanned == in.Banned {
		return &StatusResult{Person: p, Warnings: []string{}}, nil
	}
	if _, err := s.pool.Exec(ctx, `UPDATE people SET is_banned = $2, updated_at = now() WHERE id = $1`, personID, in.Banned); err != nil {
		return nil, httperror.Internal(err)
	}
	warnings := s.setLogin(ctx, p, !in.Banned && p.Status == domain.PersonActive)
	action := "person.ban"
	if !in.Banned {
		action = "person.unban"
	}
	if err := s.audit.Record(ctx, actor, action, "person", personID, map[string]any{"reason": in.Reason, "warnings": warnings}); err != nil {
		return nil, err
	}
	p, err = s.people.Get(ctx, personID)
	if err != nil {
		return nil, err
	}
	return &StatusResult{Person: p, Warnings: warnings}, nil
}

// ResendPasswordSetup emails the person a new set-password link (when the
// first one failed or expired). The realm needs SMTP.
func (s *LifecycleService) ResendPasswordSetup(ctx context.Context, actor string, personID uuid.UUID) error {
	p, err := s.people.Get(ctx, personID)
	if err != nil {
		return err
	}
	sender, ok := s.accounts.(provisioners.PasswordSetupSender)
	switch {
	case p.IdPUserID == "":
		return httperror.Conflict("this person has no account yet")
	case s.linkOnly:
		return httperror.Conflict("this installation links to the convention's own accounts; passwords are managed there")
	case !ok:
		return httperror.Conflict("the identity provider cannot send set-password emails")
	}
	if err := sender.SendPasswordSetup(ctx, p.IdPUserID); err != nil {
		if errors.Is(err, provisioners.ErrAccountNotFound) {
			return httperror.Conflict("their account no longer exists in the identity provider")
		}
		return idpFailure("send the set-password email (check the realm's SMTP settings)", err)
	}
	return s.audit.Record(ctx, actor, "person.password_setup_email", "person", personID, nil)
}

// Offboard deletes the person's account, emails them that it was deleted,
// and deletes them from HR: profile, onboardings and the audit entries
// about their profile. The access history (AccessHistoryActions) and the
// offboarding line stay for Retention.OffboardedAuditMonths, then the
// maintenance job purges them.
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
	case acc.Account.State == "linked" && s.accounts != nil && s.linkOnly:
		// The convention owns the account: take HR's groups and link away.
		remove := []string{}
		for g := range membershipGroups(acc.Memberships, uuid.Nil) {
			remove = append(remove, g)
		}
		if lead, err := s.depts.LeadGroupsFor(ctx, personID); err == nil {
			remove = append(remove, lead...)
		}
		err := s.accounts.UpdateMembership(ctx, acc.ExternalID, nil, remove)
		if err == nil {
			err = s.accounts.UpdateProfile(ctx, acc.ExternalID, provisioners.Profile{Attributes: map[string]string{provisioners.AttrHRPersonID: ""}})
		}
		if err != nil && !errors.Is(err, provisioners.ErrAccountNotFound) {
			return nil, idpFailure("remove their HR groups", err)
		}
		warnings = append(warnings, "Their account belongs to the convention's identity provider and was not deleted; HR removed its groups from it.")
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
	settings, err := s.settings.Get(ctx)
	if err != nil {
		return nil, err
	}
	keepMonths := settings.Retention.OffboardedAuditMonths
	if err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		// Profile changes go with the person; the access history (who
		// granted or removed what, when) stays until its purge date.
		if _, err := tx.Exec(ctx, `
DELETE FROM audit_events
 WHERE (entity_id = $1 OR entity_id IN (SELECT id FROM employment_drafts WHERE person_id = $1))
   AND NOT (action = ANY($2))`, personID, AccessHistoryActions); err != nil {
			return httperror.Internal(err)
		}
		if _, err := tx.Exec(ctx, `
UPDATE audit_events SET purge_after = now() + make_interval(months => $2)
 WHERE entity_id = $1 OR entity_id IN (SELECT id FROM employment_drafts WHERE person_id = $1)`, personID, keepMonths); err != nil {
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
	if _, err := s.pool.Exec(ctx, `
UPDATE audit_events SET purge_after = now() + make_interval(months => $2) WHERE entity_id = $1 AND purge_after IS NULL`,
		personID, keepMonths); err != nil {
		return nil, httperror.Internal(err)
	}
	if s.workspace != nil {
		if warn := s.workspace.Offboard(ctx, actor, acc.Person); warn != "" {
			warnings = append(warnings, warn)
		}
	}
	// Tell the other modules (the wiki orphans their private space). Only
	// meaningful when the person ever had an IdP account.
	if acc.ExternalID != "" {
		warnings = append(warnings, s.notifyOffboardHooks(ctx, firstNonEmpty(acc.Person.IdPSubject, acc.ExternalID), acc.ExternalID, username)...)
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
