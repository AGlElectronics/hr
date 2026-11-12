package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/AGlElectronics/hr/backend/internal/audit"
	"github.com/AGlElectronics/hr/backend/internal/db"
	"github.com/AGlElectronics/hr/backend/internal/domain"
	"github.com/AGlElectronics/hr/backend/internal/httperror"
	"github.com/AGlElectronics/hr/backend/internal/provisioners"
)

// DepartmentService owns departments and their lead lists. Saving a
// department makes sure its IDP groups (members, leads and default roles)
// exist, so onboarding never has to invent groups on the fly.
type DepartmentService struct {
	pool     *pgxpool.Pool
	audit    *audit.Writer
	groups   provisioners.GroupEnsurer   // nil when the IDP cannot pre-create groups
	accounts provisioners.AccountManager // nil when the IDP cannot change membership
	// hrLeadGroup is the group of the HR lead role (first of HR_LEAD_GROUPS);
	// HR gives it to everyone on a lead list.
	hrLeadGroup string
	hooks       []OffboardHook // modules told after a create/update/archive
	logger      zerolog.Logger
	// roles validates default roles against the module catalogs (nil: no check).
	roles RoleValidator
}

// RoleValidator checks role plans against the module catalogs and fills in
// their groups.
type RoleValidator interface {
	ValidateRoles(ctx context.Context, roles []RolePlanInput, keep []domain.RolePlan) ([]RolePlanInput, error)
	ValidateDefaults(ctx context.Context, roles []RolePlanInput) ([]RolePlanInput, error)
}

// SetHooks registers the modules told when a department is created, changed
// or archived (DEPARTMENT_HOOKS, same "url|token" format as OFFBOARD_HOOKS;
// e.g. Tasks keeps one space per department).
func (s *DepartmentService) SetHooks(hooks []OffboardHook, logger zerolog.Logger) {
	s.hooks = hooks
	s.logger = logger.With().Str("component", "department_hooks").Logger()
}

// SetRoleValidator checks default roles against the catalogs.
func (s *DepartmentService) SetRoleValidator(v RoleValidator) { s.roles = v }

// DepartmentHookPayload is what DEPARTMENT_HOOKS receive.
type DepartmentHookPayload struct {
	ID        string `json:"id"`
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	Group     string `json:"group"`
	LeadGroup string `json:"lead_group"`
	Archived  bool   `json:"archived"`
}

// notifyHooks tells each hook the department's current shape. A failed hook
// is logged and never blocks the save: the module catches up on the next
// save or via the backfill script.
func (s *DepartmentService) notifyHooks(ctx context.Context, d *domain.Department) {
	for _, h := range s.hooks {
		body, _ := json.Marshal(DepartmentHookPayload{
			ID: d.ID.String(), Slug: d.Slug, Name: d.Name, Group: d.GroupName, LeadGroup: d.LeadGroup, Archived: d.ArchivedAt != nil,
		})
		hctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		req, err := http.NewRequestWithContext(hctx, http.MethodPost, h.URL, bytes.NewReader(body))
		if err != nil {
			cancel()
			s.logger.Warn().Str("url", h.URL).Err(err).Msg("department hook is misconfigured")
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
		if err != nil {
			s.logger.Warn().Str("url", h.URL).Str("department", d.Slug).Err(err).Msg("department hook failed")
		} else if res.StatusCode >= 300 {
			s.logger.Warn().Str("url", h.URL).Str("department", d.Slug).Str("status", res.Status).Msg("department hook refused the update")
		}
	}
}

// NewDepartmentService wires a DepartmentService. groups and accounts may be nil.
func NewDepartmentService(pool *pgxpool.Pool, aud *audit.Writer, groups provisioners.GroupEnsurer, accounts provisioners.AccountManager, hrLeadGroup string) *DepartmentService {
	return &DepartmentService{pool: pool, audit: aud, groups: groups, accounts: accounts, hrLeadGroup: hrLeadGroup, logger: zerolog.Nop()}
}

// DepartmentInput is the writeable surface of a department.
type DepartmentInput struct {
	Name           string           `json:"name"`
	Slug           string           `json:"slug,omitempty"`
	Description    string           `json:"description,omitempty"`
	DefaultRoles   []domain.RoleRef `json:"default_roles"`
	DefaultBundles []uuid.UUID      `json:"default_bundles,omitempty"`
}

var (
	slugRE    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	nonSlugRE = regexp.MustCompile(`[^a-z0-9]+`)
)

func slugify(s string) string {
	return strings.Trim(nonSlugRE.ReplaceAllString(strings.ToLower(s), "-"), "-")
}

func (in *DepartmentInput) normalize() map[string]string {
	in.Name = strings.TrimSpace(in.Name)
	in.Description = strings.TrimSpace(in.Description)
	in.Slug = strings.TrimSpace(in.Slug)
	if in.Slug == "" {
		in.Slug = slugify(in.Name)
	}
	issues := map[string]string{}
	if in.Name == "" {
		issues["name"] = "required"
	}
	if !slugRE.MatchString(in.Slug) {
		issues["slug"] = "lowercase letters, digits and dashes only"
	}
	roles := make([]domain.RoleRef, 0, len(in.DefaultRoles))
	seen := map[string]bool{}
	for _, r := range in.DefaultRoles {
		r.RoleName = strings.TrimSpace(r.RoleName)
		r.GroupName = strings.TrimSpace(r.GroupName)
		r.Scope = strings.TrimSpace(r.Scope)
		if !strings.Contains(r.RoleName, ":") {
			issues["default_roles"] = "role_name must look like <module>:<role>"
			continue
		}
		if r.Scope == "" {
			r.Scope = "global"
		}
		if seen[r.RoleName] {
			continue
		}
		seen[r.RoleName] = true
		roles = append(roles, r)
	}
	in.DefaultRoles = roles
	if in.DefaultBundles == nil {
		in.DefaultBundles = []uuid.UUID{}
	}
	return issues
}

// validateDefaults checks default roles against the catalogs and default
// bundles against the stored access groups.
func (s *DepartmentService) validateDefaults(ctx context.Context, in *DepartmentInput, keep []domain.RoleRef) error {
	if s.roles != nil && len(in.DefaultRoles) > 0 {
		plans := make([]RolePlanInput, 0, len(in.DefaultRoles))
		for _, r := range in.DefaultRoles {
			plans = append(plans, RolePlanInput{RoleName: r.RoleName, Scope: r.Scope})
		}
		kept := make([]domain.RolePlan, 0, len(keep))
		for _, r := range keep {
			kept = append(kept, domain.RolePlan{RoleName: r.RoleName, GroupName: r.GroupName, Scope: r.Scope})
		}
		checked, err := s.roles.ValidateRoles(ctx, plans, kept)
		if err != nil {
			return err
		}
		in.DefaultRoles = in.DefaultRoles[:0]
		for _, r := range checked {
			in.DefaultRoles = append(in.DefaultRoles, domain.RoleRef{RoleName: r.RoleName, GroupName: r.GroupName, Scope: r.Scope})
		}
	}
	if len(in.DefaultBundles) > 0 {
		var n int
		if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM access_bundles WHERE id = ANY($1)`, in.DefaultBundles).Scan(&n); err != nil {
			return httperror.Internal(err)
		}
		if n != len(in.DefaultBundles) {
			return httperror.Validation("validation failed", map[string]string{"default_bundles": "unknown access group"})
		}
	}
	return nil
}

const deptCols = `id, slug, name, description, group_name, default_roles, default_bundles, idp_synced_at, COALESCE(idp_sync_error, ''),
archived_at, created_at, updated_at,
(SELECT COUNT(DISTINCT dr.person_id) FROM employment_drafts dr JOIN people p ON p.id = dr.person_id
  WHERE dr.department_id = departments.id AND dr.state = 'active' AND p.status IN ('active', 'paused'))`

func scanDepartment(row rowScanner) (*domain.Department, error) {
	d := &domain.Department{}
	var roles, bundles []byte
	if err := row.Scan(&d.ID, &d.Slug, &d.Name, &d.Description, &d.GroupName, &roles, &bundles, &d.IDPSyncedAt, &d.IDPSyncError,
		&d.ArchivedAt, &d.CreatedAt, &d.UpdatedAt, &d.MemberCount); err != nil {
		return nil, err
	}
	d.DefaultRoles = []domain.RoleRef{}
	d.DefaultBundles = []uuid.UUID{}
	d.Leads = []domain.LeadRef{}
	d.LeadGroup = domain.LeadGroupFor(d.GroupName)
	_ = json.Unmarshal(roles, &d.DefaultRoles)
	_ = json.Unmarshal(bundles, &d.DefaultBundles)
	return d, nil
}

// withLeads fills in the lead lists.
func (s *DepartmentService) withLeads(ctx context.Context, depts []*domain.Department) error {
	if len(depts) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, 0, len(depts))
	byID := map[uuid.UUID]*domain.Department{}
	for _, d := range depts {
		ids = append(ids, d.ID)
		byID[d.ID] = d
	}
	rows, err := s.pool.Query(ctx, `
SELECT dl.department_id, p.id, p.display_name FROM department_leads dl JOIN people p ON p.id = dl.person_id
 WHERE dl.department_id = ANY($1) ORDER BY lower(p.display_name)`, ids)
	if err != nil {
		return httperror.Internal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var dept uuid.UUID
		var l domain.LeadRef
		if err := rows.Scan(&dept, &l.PersonID, &l.DisplayName); err != nil {
			return httperror.Internal(err)
		}
		byID[dept].Leads = append(byID[dept].Leads, l)
	}
	return rows.Err()
}

// List returns departments ordered by name; archived ones only on request.
func (s *DepartmentService) List(ctx context.Context) ([]domain.Department, error) {
	return s.list(ctx, true)
}

// ListActive returns the departments people can still be onboarded into.
func (s *DepartmentService) ListActive(ctx context.Context) ([]domain.Department, error) {
	return s.list(ctx, false)
}

func (s *DepartmentService) list(ctx context.Context, archived bool) ([]domain.Department, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+deptCols+` FROM departments WHERE $1 OR archived_at IS NULL ORDER BY lower(name)`, archived)
	if err != nil {
		return nil, httperror.Internal(err)
	}
	defer rows.Close()
	ptrs := []*domain.Department{}
	for rows.Next() {
		d, err := scanDepartment(rows)
		if err != nil {
			return nil, httperror.Internal(err)
		}
		ptrs = append(ptrs, d)
	}
	if err := rows.Err(); err != nil {
		return nil, httperror.Internal(err)
	}
	rows.Close()
	if err := s.withLeads(ctx, ptrs); err != nil {
		return nil, err
	}
	out := make([]domain.Department, 0, len(ptrs))
	for _, d := range ptrs {
		out = append(out, *d)
	}
	return out, nil
}

// Get returns one department.
func (s *DepartmentService) Get(ctx context.Context, id uuid.UUID) (*domain.Department, error) {
	d, err := scanDepartment(s.pool.QueryRow(ctx, `SELECT `+deptCols+` FROM departments WHERE id = $1`, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, httperror.NotFound("department")
		}
		return nil, httperror.Internal(err)
	}
	if err := s.withLeads(ctx, []*domain.Department{d}); err != nil {
		return nil, err
	}
	return d, nil
}

// Create inserts a department; its IDP group is "dept-<slug>" (leads:
// "dept-<slug>-lead") and never changes afterwards, even if the department
// is renamed.
func (s *DepartmentService) Create(ctx context.Context, actor string, in DepartmentInput) (*domain.Department, error) {
	if issues := in.normalize(); len(issues) > 0 {
		return nil, httperror.Validation("validation failed", issues)
	}
	if err := s.validateDefaults(ctx, &in, nil); err != nil {
		return nil, err
	}
	roles, _ := json.Marshal(in.DefaultRoles)
	bundles, _ := json.Marshal(in.DefaultBundles)
	d, err := scanDepartment(s.pool.QueryRow(ctx, `
INSERT INTO departments (slug, name, description, group_name, default_roles, default_bundles)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING `+deptCols, in.Slug, in.Name, in.Description, "dept-"+in.Slug, roles, bundles))
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, httperror.Validation("validation failed", map[string]string{"slug": "already used by another department"})
		}
		return nil, httperror.Internal(err)
	}
	if err := s.audit.Record(ctx, actor, "department.create", "department", d.ID, map[string]any{"slug": d.Slug}); err != nil {
		return nil, err
	}
	d = s.syncIDP(ctx, d)
	s.notifyHooks(ctx, d)
	return d, nil
}

// Update changes name, description and default access (slug and group stay).
func (s *DepartmentService) Update(ctx context.Context, actor string, id uuid.UUID, in DepartmentInput) (*domain.Department, error) {
	in.Slug = "keep"
	if issues := in.normalize(); len(issues) > 0 {
		return nil, httperror.Validation("validation failed", issues)
	}
	prev, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.validateDefaults(ctx, &in, prev.DefaultRoles); err != nil {
		return nil, err
	}
	roles, _ := json.Marshal(in.DefaultRoles)
	bundles, _ := json.Marshal(in.DefaultBundles)
	d, err := scanDepartment(s.pool.QueryRow(ctx, `
UPDATE departments SET name = $2, description = $3, default_roles = $4, default_bundles = $5, updated_at = now()
 WHERE id = $1
RETURNING `+deptCols, id, in.Name, in.Description, roles, bundles))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, httperror.NotFound("department")
		}
		return nil, httperror.Internal(err)
	}
	if err := s.audit.Record(ctx, actor, "department.update", "department", d.ID, map[string]any{
		"name": d.Name, "default_roles": len(d.DefaultRoles), "default_bundles": len(d.DefaultBundles),
	}); err != nil {
		return nil, err
	}
	d = s.syncIDP(ctx, d)
	s.notifyHooks(ctx, d)
	return s.Get(ctx, d.ID)
}

// SetArchived archives a department (no new onboardings; existing members
// keep their access) or restores it, and tells the department hooks.
func (s *DepartmentService) SetArchived(ctx context.Context, actor string, id uuid.UUID, archived bool) (*domain.Department, error) {
	tag, err := s.pool.Exec(ctx, `
UPDATE departments SET archived_at = CASE WHEN $2 THEN COALESCE(archived_at, now()) ELSE NULL END, updated_at = now()
 WHERE id = $1`, id, archived)
	if err != nil {
		return nil, httperror.Internal(err)
	}
	if tag.RowsAffected() == 0 {
		return nil, httperror.NotFound("department")
	}
	action := "department.archive"
	if !archived {
		action = "department.restore"
	}
	if err := s.audit.Record(ctx, actor, action, "department", id, nil); err != nil {
		return nil, err
	}
	d, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	s.notifyHooks(ctx, d)
	return d, nil
}

// LeadsResult is the department after a lead change plus anything HR could
// not do in the IDP.
type LeadsResult struct {
	Department *domain.Department `json:"department"`
	Warnings   []string           `json:"warnings"`
}

// SetLeads replaces the department's lead list. Leads get the department's
// lead group and the HR lead group in the IDP; people removed from every
// lead list lose the HR lead group again.
func (s *DepartmentService) SetLeads(ctx context.Context, actor string, id uuid.UUID, personIDs []uuid.UUID) (*LeadsResult, error) {
	d, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	want := map[uuid.UUID]bool{}
	for _, p := range personIDs {
		want[p] = true
	}
	ids := make([]uuid.UUID, 0, len(want))
	for p := range want {
		ids = append(ids, p)
	}
	if len(ids) > 0 {
		var n int
		if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM people WHERE id = ANY($1)`, ids).Scan(&n); err != nil {
			return nil, httperror.Internal(err)
		}
		if n != len(ids) {
			return nil, httperror.Validation("validation failed", map[string]string{"person_ids": "unknown person"})
		}
	}
	had := map[uuid.UUID]bool{}
	for _, l := range d.Leads {
		had[l.PersonID] = true
	}
	var added, removed []uuid.UUID
	for p := range want {
		if !had[p] {
			added = append(added, p)
		}
	}
	for p := range had {
		if !want[p] {
			removed = append(removed, p)
		}
	}
	if err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM department_leads WHERE department_id = $1 AND NOT (person_id = ANY($2))`, id, ids); err != nil {
			return httperror.Internal(err)
		}
		for _, p := range added {
			if _, err := tx.Exec(ctx, `INSERT INTO department_leads (department_id, person_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, id, p); err != nil {
				return httperror.Internal(err)
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	warnings := s.syncLeadGroups(ctx, d, added, removed)
	if err := s.audit.Record(ctx, actor, "department.leads", "department", id, map[string]any{"added": added, "removed": removed}); err != nil {
		return nil, err
	}
	d, err = s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return &LeadsResult{Department: d, Warnings: warnings}, nil
}

// syncLeadGroups mirrors a lead change onto the IDP accounts.
func (s *DepartmentService) syncLeadGroups(ctx context.Context, d *domain.Department, added, removed []uuid.UUID) []string {
	warnings := []string{}
	if s.accounts == nil {
		return warnings
	}
	apply := func(personID uuid.UUID, add, remove []string) {
		var ext, name string
		if err := s.pool.QueryRow(ctx, `SELECT COALESCE(idp_user_id, ''), display_name FROM people WHERE id = $1`, personID).Scan(&ext, &name); err != nil {
			warnings = append(warnings, "Could not look up a lead: "+err.Error())
			return
		}
		if ext == "" {
			warnings = append(warnings, name+" has no account yet; they get the lead groups when they are onboarded.")
			return
		}
		if err := s.accounts.UpdateMembership(ctx, ext, add, remove); err != nil {
			warnings = append(warnings, "Could not update the lead groups of "+name+": "+err.Error())
		}
	}
	for _, p := range added {
		apply(p, nonEmptyStrings(d.LeadGroup, s.hrLeadGroup), nil)
	}
	for _, p := range removed {
		remove := []string{d.LeadGroup}
		var stillLead bool
		_ = s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM department_leads WHERE person_id = $1)`, p).Scan(&stillLead)
		if !stillLead && s.hrLeadGroup != "" {
			remove = append(remove, s.hrLeadGroup)
		}
		apply(p, nil, remove)
	}
	return warnings
}

// LeadGroupsFor returns the lead groups a person should hold (their
// departments' lead groups plus the HR lead group), used when their account
// is created after they were put on a lead list.
func (s *DepartmentService) LeadGroupsFor(ctx context.Context, personID uuid.UUID) ([]string, error) {
	rows, err := s.pool.Query(ctx, `
SELECT d.group_name FROM department_leads dl JOIN departments d ON d.id = dl.department_id WHERE dl.person_id = $1`, personID)
	if err != nil {
		return nil, httperror.Internal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var g string
		if err := rows.Scan(&g); err != nil {
			return nil, httperror.Internal(err)
		}
		out = append(out, domain.LeadGroupFor(g))
	}
	if len(out) > 0 && s.hrLeadGroup != "" {
		out = append(out, s.hrLeadGroup)
	}
	return out, rows.Err()
}

func nonEmptyStrings(ss ...string) []string {
	out := []string{}
	for _, s := range ss {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// syncIDP creates the department groups and its default role groups. A
// failure is recorded on the department rather than failing the save, so
// HR can keep working while Keycloak is unreachable; saving again retries.
func (s *DepartmentService) syncIDP(ctx context.Context, d *domain.Department) *domain.Department {
	if s.groups == nil {
		return d
	}
	names := []string{d.GroupName, d.LeadGroup}
	for _, r := range d.DefaultRoles {
		names = append(names, r.GroupName)
	}
	syncErr := s.groups.EnsureGroups(ctx, names)
	msg := ""
	if syncErr != nil {
		msg = syncErr.Error()
	}
	updated, err := scanDepartment(s.pool.QueryRow(ctx, `
UPDATE departments
   SET idp_synced_at = CASE WHEN $2 = '' THEN now() ELSE idp_synced_at END,
       idp_sync_error = NULLIF($2, '')
 WHERE id = $1
RETURNING `+deptCols, d.ID, msg))
	if err != nil {
		return d
	}
	updated.Leads = d.Leads
	return updated
}
