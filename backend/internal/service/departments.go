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
	"github.com/AGlElectronics/hr/backend/internal/domain"
	"github.com/AGlElectronics/hr/backend/internal/httperror"
	"github.com/AGlElectronics/hr/backend/internal/provisioners"
)

// DepartmentService owns departments. Saving a department makes sure its IdP
// group (and the groups of its default roles) exist, so onboarding never has
// to invent groups on the fly.
type DepartmentService struct {
	pool   *pgxpool.Pool
	audit  *audit.Writer
	groups provisioners.GroupEnsurer // nil when the IdP cannot pre-create groups
	hooks  []OffboardHook            // modules told after a create/rename
	logger zerolog.Logger
}

// SetHooks registers the modules told when a department is created or
// renamed (DEPARTMENT_HOOKS, same "url|token" format as OFFBOARD_HOOKS;
// e.g. Tasks keeps one space per department).
func (s *DepartmentService) SetHooks(hooks []OffboardHook, logger zerolog.Logger) {
	s.hooks = hooks
	s.logger = logger.With().Str("component", "department_hooks").Logger()
}

// notifyHooks tells each hook the department's current shape. A failed hook
// is logged and never blocks the save: the module catches up on the next
// save or via the backfill script.
func (s *DepartmentService) notifyHooks(ctx context.Context, d *domain.Department) {
	for _, h := range s.hooks {
		body, _ := json.Marshal(map[string]string{
			"id": d.ID.String(), "slug": d.Slug, "name": d.Name, "group": d.GroupName,
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

// NewDepartmentService wires a DepartmentService.
func NewDepartmentService(pool *pgxpool.Pool, aud *audit.Writer, groups provisioners.GroupEnsurer) *DepartmentService {
	return &DepartmentService{pool: pool, audit: aud, groups: groups}
}

// DepartmentInput is the writeable surface of a department.
type DepartmentInput struct {
	Name         string           `json:"name"`
	Slug         string           `json:"slug,omitempty"`
	Description  string           `json:"description,omitempty"`
	DefaultRoles []domain.RoleRef `json:"default_roles"`
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
	return issues
}

const deptCols = `id, slug, name, description, group_name, default_roles, idp_synced_at, COALESCE(idp_sync_error, ''), created_at, updated_at,
(SELECT COUNT(DISTINCT dr.person_id) FROM employment_drafts dr JOIN people p ON p.id = dr.person_id
  WHERE dr.department_id = departments.id AND dr.state = 'active' AND p.status IN ('active', 'paused'))`

func scanDepartment(row rowScanner) (*domain.Department, error) {
	d := &domain.Department{}
	var roles []byte
	if err := row.Scan(&d.ID, &d.Slug, &d.Name, &d.Description, &d.GroupName, &roles, &d.IdPSyncedAt, &d.IdPSyncError, &d.CreatedAt, &d.UpdatedAt, &d.MemberCount); err != nil {
		return nil, err
	}
	d.DefaultRoles = []domain.RoleRef{}
	_ = json.Unmarshal(roles, &d.DefaultRoles)
	return d, nil
}

// List returns every department ordered by name.
func (s *DepartmentService) List(ctx context.Context) ([]domain.Department, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+deptCols+` FROM departments ORDER BY lower(name)`)
	if err != nil {
		return nil, httperror.Internal(err)
	}
	defer rows.Close()
	out := make([]domain.Department, 0, 8)
	for rows.Next() {
		d, err := scanDepartment(rows)
		if err != nil {
			return nil, httperror.Internal(err)
		}
		out = append(out, *d)
	}
	if err := rows.Err(); err != nil {
		return nil, httperror.Internal(err)
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
	return d, nil
}

// Create inserts a department; its IdP group is "dept-<slug>" and never
// changes afterwards, even if the department is renamed.
func (s *DepartmentService) Create(ctx context.Context, actor string, in DepartmentInput) (*domain.Department, error) {
	if issues := in.normalize(); len(issues) > 0 {
		return nil, httperror.Validation("validation failed", issues)
	}
	roles, _ := json.Marshal(in.DefaultRoles)
	d, err := scanDepartment(s.pool.QueryRow(ctx, `
INSERT INTO departments (slug, name, description, group_name, default_roles)
VALUES ($1, $2, $3, $4, $5)
RETURNING `+deptCols, in.Slug, in.Name, in.Description, "dept-"+in.Slug, roles))
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
	d = s.syncIdP(ctx, d)
	s.notifyHooks(ctx, d)
	return d, nil
}

// Update changes name, description and default access (slug and group stay).
func (s *DepartmentService) Update(ctx context.Context, actor string, id uuid.UUID, in DepartmentInput) (*domain.Department, error) {
	in.Slug = "keep"
	if issues := in.normalize(); len(issues) > 0 {
		return nil, httperror.Validation("validation failed", issues)
	}
	roles, _ := json.Marshal(in.DefaultRoles)
	d, err := scanDepartment(s.pool.QueryRow(ctx, `
UPDATE departments SET name = $2, description = $3, default_roles = $4, updated_at = now()
 WHERE id = $1
RETURNING `+deptCols, id, in.Name, in.Description, roles))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, httperror.NotFound("department")
		}
		return nil, httperror.Internal(err)
	}
	if err := s.audit.Record(ctx, actor, "department.update", "department", d.ID, map[string]any{
		"default_roles": len(d.DefaultRoles),
	}); err != nil {
		return nil, err
	}
	d = s.syncIdP(ctx, d)
	s.notifyHooks(ctx, d)
	return d, nil
}

// syncIdP creates the department group and its default role groups. A
// failure is recorded on the department rather than failing the save, so
// HR can keep working while Keycloak is unreachable; saving again retries.
func (s *DepartmentService) syncIdP(ctx context.Context, d *domain.Department) *domain.Department {
	if s.groups == nil {
		return d
	}
	names := []string{d.GroupName}
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
	return updated
}
