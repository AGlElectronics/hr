package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AGlElectronics/hr/backend/internal/audit"
	"github.com/AGlElectronics/hr/backend/internal/db"
	"github.com/AGlElectronics/hr/backend/internal/domain"
	"github.com/AGlElectronics/hr/backend/internal/httperror"
)

// DraftService owns employment_drafts and role_plans. Approvals and
// provisioning jobs are separate services; they just read drafts and
// invoke transitions here.
type DraftService struct {
	pool    *pgxpool.Pool
	audit   *audit.Writer
	bundles *BundleService // plans roles and access groups; nil: no checks
}

// SetBundles plans role assignments through the catalogs and access groups.
func (s *DraftService) SetBundles(b *BundleService) { s.bundles = b }

// NewDraftService wires a DraftService.
func NewDraftService(pool *pgxpool.Pool, aud *audit.Writer) *DraftService {
	return &DraftService{pool: pool, audit: aud}
}

// CreateDraftInput is the writeable surface of a new employment draft.
type CreateDraftInput struct {
	PersonID     uuid.UUID  `json:"person_id"`
	DepartmentID *uuid.UUID `json:"department_id,omitempty"`
	Title        string     `json:"title"`
	StartDate    *time.Time `json:"start_date,omitempty"`
	EndDate      *time.Time `json:"end_date,omitempty"`
	Notes        string     `json:"notes,omitempty"`
}

// AssignRolesInput sets the access of a draft: individually picked roles
// plus access groups, replacing what was assigned before.
type AssignRolesInput struct {
	Roles     []RolePlanInput `json:"roles"`
	BundleIDs []uuid.UUID     `json:"bundle_ids,omitempty"`
}

// RolePlanInput is one requested role on a draft. GroupName is ignored on
// input: HR always uses the group from the module's catalog.
type RolePlanInput struct {
	RoleName  string `json:"role_name"`
	GroupName string `json:"group_name,omitempty"`
	Scope     string `json:"scope,omitempty"`
	// bundle is the access group the role comes from (nil: individual).
	bundle *uuid.UUID
}

// Create inserts a new draft in state "draft".
func (s *DraftService) Create(ctx context.Context, actor string, in CreateDraftInput) (*domain.EmploymentDraft, error) {
	in.Title = strings.TrimSpace(in.Title)
	in.Notes = strings.TrimSpace(in.Notes)
	if in.PersonID == uuid.Nil {
		return nil, httperror.Validation("person_id is required", nil)
	}
	if in.Title == "" && in.DepartmentID != nil {
		title, err := s.defaultTitle(ctx, *in.DepartmentID)
		if err != nil {
			return nil, err
		}
		in.Title = title
	}
	if in.Title == "" {
		return nil, httperror.Validation("title is required", nil)
	}

	// Reject drafts for unknown people up front so we don't need to translate
	// an FK violation into something user-friendly later.
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM people WHERE id=$1)`, in.PersonID).Scan(&exists); err != nil {
		return nil, httperror.Internal(err)
	}
	if !exists {
		return nil, httperror.NotFound("person")
	}

	id := uuid.New()
	now := time.Now().UTC()
	const q = `
INSERT INTO employment_drafts (id, person_id, title, start_date, end_date, state, notes, created_by, created_at, updated_at, department_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9, $10)
RETURNING id, person_id, department_id, title, start_date, end_date, state, COALESCE(notes, ''), created_by, created_at, updated_at
`
	d := &domain.EmploymentDraft{}
	err := s.pool.QueryRow(ctx, q, id, in.PersonID, in.Title, in.StartDate, in.EndDate, string(domain.DraftStateDraft), in.Notes, actor, now, in.DepartmentID).
		Scan(&d.ID, &d.PersonID, &d.DepartmentID, &d.Title, &d.StartDate, &d.EndDate, &d.State, &d.Notes, &d.CreatedBy, &d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		return nil, httperror.Internal(err)
	}

	if err := s.audit.Record(ctx, actor, "draft.create", "employment_draft", d.ID, map[string]any{
		"person_id": d.PersonID,
		"title":     d.Title,
	}); err != nil {
		return nil, err
	}
	return d, nil
}

func (s *DraftService) defaultTitle(ctx context.Context, departmentID uuid.UUID) (string, error) {
	var name string
	if err := s.pool.QueryRow(ctx, `SELECT name FROM departments WHERE id = $1`, departmentID).Scan(&name); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", httperror.NotFound("department")
		}
		return "", httperror.Internal(err)
	}
	return name + " volunteer", nil
}

// SetDepartment moves a still-editable draft to another department and
// resets its title to that department's default.
func (s *DraftService) SetDepartment(ctx context.Context, actor string, draftID, departmentID uuid.UUID) (*domain.EmploymentDraft, error) {
	title, err := s.defaultTitle(ctx, departmentID)
	if err != nil {
		return nil, err
	}
	tag, err := s.pool.Exec(ctx, `
UPDATE employment_drafts SET department_id = $2, title = $3, updated_at = now()
 WHERE id = $1 AND state = 'draft'`, draftID, departmentID, title)
	if err != nil {
		return nil, httperror.Internal(err)
	}
	if tag.RowsAffected() == 0 {
		return nil, httperror.Conflict("department can only be changed while the onboarding is still a draft")
	}
	if err := s.audit.Record(ctx, actor, "draft.set_department", "employment_draft", draftID, map[string]any{
		"department_id": departmentID,
	}); err != nil {
		return nil, err
	}
	return s.Get(ctx, draftID)
}

// Get returns a single draft with its role plans attached.
func (s *DraftService) Get(ctx context.Context, id uuid.UUID) (*domain.EmploymentDraft, error) {
	const q = `
SELECT id, person_id, department_id, title, start_date, end_date, state, COALESCE(notes, ''), created_by, created_at, updated_at
FROM employment_drafts WHERE id = $1
`
	d := &domain.EmploymentDraft{}
	err := s.pool.QueryRow(ctx, q, id).
		Scan(&d.ID, &d.PersonID, &d.DepartmentID, &d.Title, &d.StartDate, &d.EndDate, &d.State, &d.Notes, &d.CreatedBy, &d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, httperror.NotFound("employment_draft")
		}
		return nil, httperror.Internal(err)
	}

	roles, err := s.listRoles(ctx, id)
	if err != nil {
		return nil, err
	}
	d.Roles = roles
	rows, err := s.pool.Query(ctx, `SELECT bundle_id FROM draft_bundles WHERE draft_id = $1 ORDER BY bundle_id`, id)
	if err != nil {
		return nil, httperror.Internal(err)
	}
	defer rows.Close()
	d.BundleIDs = []uuid.UUID{}
	for rows.Next() {
		var b uuid.UUID
		if err := rows.Scan(&b); err != nil {
			return nil, httperror.Internal(err)
		}
		d.BundleIDs = append(d.BundleIDs, b)
	}
	return d, rows.Err()
}

// ListDraftsInput filters and pages the onboarding list.
type ListDraftsInput struct {
	State string
	// Query matches the person's names and email.
	Query string
	// DepartmentIDs, when not nil, limits the list to these departments.
	DepartmentIDs []uuid.UUID
	Limit, Offset int
}

// List returns one page of drafts, newest first, with the person's and
// department's names.
func (s *DraftService) List(ctx context.Context, in ListDraftsInput) (*Page[domain.EmploymentDraft], error) {
	in.Limit, in.Offset = NormalizePaging(in.Limit, in.Offset)
	q := "%" + strings.ToLower(strings.TrimSpace(in.Query)) + "%"
	scoped := in.DepartmentIDs != nil
	if in.DepartmentIDs == nil {
		in.DepartmentIDs = []uuid.UUID{}
	}
	rows, err := s.pool.Query(ctx, `
SELECT d.id, d.person_id, d.department_id, d.title, d.start_date, d.end_date, d.state, COALESCE(d.notes, ''), d.created_by,
       d.created_at, d.updated_at, p.display_name, COALESCE(dep.name, ''), count(*) OVER ()
  FROM employment_drafts d
  JOIN people p ON p.id = d.person_id
  LEFT JOIN departments dep ON dep.id = d.department_id
 WHERE ($1 = '' OR d.state = $1)
   AND ($2 = '%%' OR lower(p.legal_name) LIKE $2 OR lower(p.display_name) LIKE $2 OR lower(p.email) LIKE $2)
   AND (NOT $3 OR d.department_id = ANY($4))
 ORDER BY d.updated_at DESC, d.id
 LIMIT $5 OFFSET $6`, strings.TrimSpace(in.State), q, scoped, in.DepartmentIDs, in.Limit, in.Offset)
	if err != nil {
		return nil, httperror.Internal(err)
	}
	defer rows.Close()
	out := &Page[domain.EmploymentDraft]{Items: []domain.EmploymentDraft{}, Limit: in.Limit, Offset: in.Offset}
	for rows.Next() {
		var d domain.EmploymentDraft
		if err := rows.Scan(&d.ID, &d.PersonID, &d.DepartmentID, &d.Title, &d.StartDate, &d.EndDate, &d.State, &d.Notes, &d.CreatedBy,
			&d.CreatedAt, &d.UpdatedAt, &d.PersonName, &d.DepartmentName, &out.Total); err != nil {
			return nil, httperror.Internal(err)
		}
		out.Items = append(out.Items, d)
	}
	if err := rows.Err(); err != nil {
		return nil, httperror.Internal(err)
	}
	return out, nil
}

// AssignRoles replaces the role plans attached to a draft. Only legal in
// the "draft" state — once provisioning starts, role plans are frozen so
// the audit trail matches what was actually pushed to the IdP.
func (s *DraftService) AssignRoles(ctx context.Context, actor string, draftID uuid.UUID, in AssignRolesInput) ([]domain.RolePlan, error) {
	return s.assign(ctx, actor, draftID, in, false)
}

// AssignDefaults sets the access an HR admin configured as defaults
// (department and suite defaults); admin roles among them need no
// "assign admin roles" permission from the caller.
func (s *DraftService) AssignDefaults(ctx context.Context, actor string, draftID uuid.UUID, in AssignRolesInput) ([]domain.RolePlan, error) {
	return s.assign(ctx, actor, draftID, in, true)
}

func (s *DraftService) assign(ctx context.Context, actor string, draftID uuid.UUID, in AssignRolesInput, trusted bool) ([]domain.RolePlan, error) {
	normalized := make([]RolePlanInput, 0, len(in.Roles))
	for _, r := range in.Roles {
		r.RoleName = strings.TrimSpace(r.RoleName)
		r.GroupName = strings.TrimSpace(r.GroupName)
		r.Scope = strings.TrimSpace(r.Scope)
		if r.RoleName == "" {
			return nil, httperror.Validation("each role requires role_name", nil)
		}
		if r.Scope == "" {
			r.Scope = "global"
		}
		normalized = append(normalized, r)
	}
	bundleIDs := uniqueIDs(in.BundleIDs)
	if s.bundles != nil {
		current, err := s.listRoles(ctx, draftID)
		if err != nil {
			return nil, err
		}
		if normalized, bundleIDs, err = s.bundles.Plan(ctx, normalized, in.BundleIDs, current, trusted); err != nil {
			return nil, err
		}
	}

	var out []domain.RolePlan
	err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		var state string
		if err := tx.QueryRow(ctx, `SELECT state FROM employment_drafts WHERE id = $1 FOR UPDATE`, draftID).Scan(&state); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return httperror.NotFound("employment_draft")
			}
			return httperror.Internal(err)
		}
		if domain.DraftState(state) != domain.DraftStateDraft {
			return httperror.Conflict("roles can only be assigned while the draft is in state 'draft'")
		}
		return writePlans(ctx, tx, draftID, normalized, bundleIDs)
	})
	if err != nil {
		return nil, err
	}

	out, err = s.listRoles(ctx, draftID)
	if err != nil {
		return nil, err
	}
	if err := s.audit.Record(ctx, actor, "draft.assign_roles", "employment_draft", draftID, map[string]any{
		"roles": len(out), "bundles": bundleIDs,
	}); err != nil {
		return nil, err
	}
	return out, nil
}

// Transition moves a draft between states inside a transaction, enforcing the
// state machine defined in domain.CanTransition. Returns the new state.
func (s *DraftService) Transition(ctx context.Context, draftID uuid.UUID, to domain.DraftState) (*domain.EmploymentDraft, error) {
	var updated *domain.EmploymentDraft
	err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		var state string
		if err := tx.QueryRow(ctx, `SELECT state FROM employment_drafts WHERE id = $1 FOR UPDATE`, draftID).Scan(&state); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return httperror.NotFound("employment_draft")
			}
			return httperror.Internal(err)
		}
		if _, err := domain.Transition(domain.DraftState(state), to); err != nil {
			return httperror.Conflict(err.Error())
		}
		d := &domain.EmploymentDraft{}
		if err := tx.QueryRow(ctx, `
UPDATE employment_drafts
   SET state = $1, updated_at = now()
 WHERE id = $2
 RETURNING id, person_id, department_id, title, start_date, end_date, state, COALESCE(notes, ''), created_by, created_at, updated_at`, string(to), draftID).
			Scan(&d.ID, &d.PersonID, &d.DepartmentID, &d.Title, &d.StartDate, &d.EndDate, &d.State, &d.Notes, &d.CreatedBy, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return httperror.Internal(err)
		}
		updated = d
		return nil
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}

func (s *DraftService) listRoles(ctx context.Context, draftID uuid.UUID) ([]domain.RolePlan, error) {
	rows, err := s.pool.Query(ctx, `
SELECT rp.id, rp.draft_id, rp.role_name, COALESCE(rp.group_name, ''), COALESCE(rp.scope, ''), rp.created_at, rp.bundle_id, COALESCE(b.name, '')
  FROM role_plans rp LEFT JOIN access_bundles b ON b.id = rp.bundle_id
 WHERE rp.draft_id = $1 ORDER BY rp.role_name`, draftID)
	if err != nil {
		return nil, httperror.Internal(err)
	}
	defer rows.Close()
	out := make([]domain.RolePlan, 0, 4)
	for rows.Next() {
		var r domain.RolePlan
		if err := rows.Scan(&r.ID, &r.DraftID, &r.RoleName, &r.GroupName, &r.Scope, &r.CreatedAt, &r.BundleID, &r.BundleName); err != nil {
			return nil, httperror.Internal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, httperror.Internal(err)
	}
	return out, nil
}
