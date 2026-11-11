package service

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AGlElectronics/hr/backend/internal/audit"
	"github.com/AGlElectronics/hr/backend/internal/catalog"
	"github.com/AGlElectronics/hr/backend/internal/domain"
	"github.com/AGlElectronics/hr/backend/internal/httperror"
)

// Bundle is an access group: a named set of module roles HR admins define
// ("Standard volunteer" = den member + stash scanner + …). Onboarding and
// the person page assign bundles; HR expands them into the individual
// groups on the account.
type Bundle struct {
	ID          uuid.UUID        `json:"id"`
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Roles       []domain.RoleRef `json:"roles"`
	// Members counts memberships and onboardings holding the bundle.
	Members   int       `json:"members"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// BundleInput is the writeable surface of a bundle.
type BundleInput struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Roles       []RolePlanInput `json:"roles"`
}

// BundleSyncFunc applies a changed bundle to everyone holding it (queued as
// a background job).
type BundleSyncFunc func(ctx context.Context, actor string, bundleID uuid.UUID) error

// BundleService owns access bundles.
type BundleService struct {
	pool      *pgxpool.Pool
	audit     *audit.Writer
	validator *AccessValidator
	// enqueueSync queues the update of every holder after an edit.
	enqueueSync BundleSyncFunc
}

// NewBundleService wires a BundleService.
func NewBundleService(pool *pgxpool.Pool, aud *audit.Writer, v *AccessValidator) *BundleService {
	return &BundleService{pool: pool, audit: aud, validator: v}
}

// SetSync registers how an edited bundle reaches its holders.
func (s *BundleService) SetSync(f BundleSyncFunc) { s.enqueueSync = f }

const bundleCols = `b.id, b.name, b.description, b.roles, b.created_at, b.updated_at,
(SELECT count(*) FROM draft_bundles db JOIN employment_drafts d ON d.id = db.draft_id
  WHERE db.bundle_id = b.id AND d.state IN ('draft', 'provisioning', 'active'))`

func scanBundle(row rowScanner) (*Bundle, error) {
	b := &Bundle{}
	var roles []byte
	if err := row.Scan(&b.ID, &b.Name, &b.Description, &roles, &b.CreatedAt, &b.UpdatedAt, &b.Members); err != nil {
		return nil, err
	}
	b.Roles = []domain.RoleRef{}
	_ = json.Unmarshal(roles, &b.Roles)
	return b, nil
}

// List returns every bundle by name.
func (s *BundleService) List(ctx context.Context) ([]Bundle, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+bundleCols+` FROM access_bundles b ORDER BY lower(b.name)`)
	if err != nil {
		return nil, httperror.Internal(err)
	}
	defer rows.Close()
	out := []Bundle{}
	for rows.Next() {
		b, err := scanBundle(rows)
		if err != nil {
			return nil, httperror.Internal(err)
		}
		out = append(out, *b)
	}
	return out, rows.Err()
}

// Get returns one bundle.
func (s *BundleService) Get(ctx context.Context, id uuid.UUID) (*Bundle, error) {
	b, err := scanBundle(s.pool.QueryRow(ctx, `SELECT `+bundleCols+` FROM access_bundles b WHERE b.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, httperror.NotFound("access group")
	}
	if err != nil {
		return nil, httperror.Internal(err)
	}
	return b, nil
}

func (s *BundleService) normalize(ctx context.Context, in *BundleInput, keep []domain.RoleRef) ([]domain.RoleRef, error) {
	in.Name, in.Description = strings.TrimSpace(in.Name), strings.TrimSpace(in.Description)
	if in.Name == "" {
		return nil, httperror.Validation("validation failed", map[string]string{"name": "required"})
	}
	seen := map[string]bool{}
	uniq := []RolePlanInput{}
	for _, r := range in.Roles {
		r.RoleName = strings.TrimSpace(r.RoleName)
		if r.RoleName == "" || seen[r.RoleName] {
			continue
		}
		seen[r.RoleName] = true
		uniq = append(uniq, r)
	}
	kept := make([]domain.RolePlan, 0, len(keep))
	for _, r := range keep {
		kept = append(kept, domain.RolePlan{RoleName: r.RoleName, GroupName: r.GroupName, Scope: r.Scope})
	}
	checked, err := s.validator.ValidateRoles(ctx, uniq, kept)
	if err != nil {
		return nil, err
	}
	out := make([]domain.RoleRef, 0, len(checked))
	for _, r := range checked {
		out = append(out, domain.RoleRef{RoleName: r.RoleName, GroupName: r.GroupName, Scope: r.Scope})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RoleName < out[j].RoleName })
	return out, nil
}

func uniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// Create adds a bundle.
func (s *BundleService) Create(ctx context.Context, actor string, in BundleInput) (*Bundle, error) {
	roles, err := s.normalize(ctx, &in, nil)
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(roles)
	var id uuid.UUID
	err = s.pool.QueryRow(ctx, `INSERT INTO access_bundles (name, description, roles) VALUES ($1, $2, $3) RETURNING id`,
		in.Name, in.Description, raw).Scan(&id)
	if uniqueViolation(err) {
		return nil, httperror.Validation("validation failed", map[string]string{"name": "another access group has this name"})
	}
	if err != nil {
		return nil, httperror.Internal(err)
	}
	if err := s.audit.Record(ctx, actor, "bundle.create", "access_bundle", id, map[string]any{"name": in.Name, "roles": roleNames(roles)}); err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

// Update changes a bundle and queues the update of everyone holding it.
func (s *BundleService) Update(ctx context.Context, actor string, id uuid.UUID, in BundleInput) (*Bundle, error) {
	prev, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	roles, err := s.normalize(ctx, &in, prev.Roles)
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(roles)
	_, err = s.pool.Exec(ctx, `UPDATE access_bundles SET name = $2, description = $3, roles = $4, updated_at = now() WHERE id = $1`,
		id, in.Name, in.Description, raw)
	if uniqueViolation(err) {
		return nil, httperror.Validation("validation failed", map[string]string{"name": "another access group has this name"})
	}
	if err != nil {
		return nil, httperror.Internal(err)
	}
	added, removed := diffRoleNames(prev.Roles, roles)
	if err := s.audit.Record(ctx, actor, "bundle.update", "access_bundle", id, map[string]any{
		"name": in.Name, "added_roles": added, "removed_roles": removed,
	}); err != nil {
		return nil, err
	}
	if (len(added) > 0 || len(removed) > 0) && s.enqueueSync != nil && prev.Members > 0 {
		if err := s.enqueueSync(ctx, actor, id); err != nil {
			return nil, err
		}
	}
	return s.Get(ctx, id)
}

// Delete removes a bundle nobody holds.
func (s *BundleService) Delete(ctx context.Context, actor string, id uuid.UUID) error {
	b, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	var inDept bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM departments WHERE default_bundles ? $1)`, id.String()).Scan(&inDept); err != nil {
		return httperror.Internal(err)
	}
	if b.Members > 0 || inDept {
		return httperror.Conflict("this access group is still assigned to people or a department; remove it there first")
	}
	if _, err := s.pool.Exec(ctx, `DELETE FROM draft_bundles WHERE bundle_id = $1`, id); err != nil {
		return httperror.Internal(err)
	}
	if _, err := s.pool.Exec(ctx, `DELETE FROM access_bundles WHERE id = $1`, id); err != nil {
		return httperror.Internal(err)
	}
	return s.audit.Record(ctx, actor, "bundle.delete", "access_bundle", id, map[string]any{"name": b.Name})
}

// Holders lists the onboardings and memberships holding a bundle.
func (s *BundleService) Holders(ctx context.Context, id uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.pool.Query(ctx, `
SELECT db.draft_id FROM draft_bundles db JOIN employment_drafts d ON d.id = db.draft_id
 WHERE db.bundle_id = $1 AND d.state IN ('draft', 'active')`, id)
	if err != nil {
		return nil, httperror.Internal(err)
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var d uuid.UUID
		if err := rows.Scan(&d); err != nil {
			return nil, httperror.Internal(err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DraftBundles returns the bundles an onboarding or membership holds.
func (s *BundleService) DraftBundles(ctx context.Context, draftID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.pool.Query(ctx, `SELECT bundle_id FROM draft_bundles WHERE draft_id = $1 ORDER BY bundle_id`, draftID)
	if err != nil {
		return nil, httperror.Internal(err)
	}
	defer rows.Close()
	out := []uuid.UUID{}
	for rows.Next() {
		var b uuid.UUID
		if err := rows.Scan(&b); err != nil {
			return nil, httperror.Internal(err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// Plan works out the role plans of an onboarding or membership: the
// individually picked roles plus the roles of each bundle. A role picked
// individually wins over the same role from a bundle. Individual roles are
// checked against the catalogs (keep: already assigned); bundle roles were
// checked when the admin saved the bundle, and roles a module has retired
// since are skipped unless already held.
func (s *BundleService) Plan(ctx context.Context, roles []RolePlanInput, bundleIDs []uuid.UUID, keep []domain.RolePlan, trusted bool) ([]RolePlanInput, []uuid.UUID, error) {
	individual := make([]RolePlanInput, 0, len(roles))
	seen := map[string]bool{}
	for _, r := range roles {
		r.RoleName = strings.TrimSpace(r.RoleName)
		if r.RoleName == "" {
			return nil, nil, httperror.Validation("each role requires role_name", nil)
		}
		if seen[r.RoleName] {
			continue
		}
		seen[r.RoleName] = true
		r.bundle = nil
		individual = append(individual, r)
	}
	var err error
	if trusted {
		individual, err = s.validator.ValidateDefaults(ctx, individual)
	} else {
		individual, err = s.validator.ValidateRoles(ctx, individual, keep)
	}
	if err != nil {
		return nil, nil, err
	}
	ids := uniqueIDs(bundleIDs)
	if len(ids) == 0 {
		return individual, ids, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT id, roles FROM access_bundles WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, nil, httperror.Internal(err)
	}
	defer rows.Close()
	found := 0
	ix := s.validator.Index(ctx)
	kept := map[string]bool{}
	for _, k := range keep {
		kept[k.RoleName] = true
	}
	out := individual
	byBundle := map[uuid.UUID][]domain.RoleRef{}
	for rows.Next() {
		var id uuid.UUID
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, nil, httperror.Internal(err)
		}
		found++
		var refs []domain.RoleRef
		_ = json.Unmarshal(raw, &refs)
		byBundle[id] = refs
	}
	if found != len(ids) {
		return nil, nil, httperror.Validation("validation failed", map[string]string{"bundle_ids": "unknown access group"})
	}
	for _, id := range ids {
		for _, ref := range byBundle[id] {
			if seen[ref.RoleName] {
				continue
			}
			role, state := ix.Lookup(ref.RoleName)
			group := ref.GroupName
			switch {
			case state == catalog.RoleKnown && role.Group != "":
				group = role.Group
			case kept[ref.RoleName]:
			default:
				continue
			}
			seen[ref.RoleName] = true
			bid := id
			out = append(out, RolePlanInput{RoleName: ref.RoleName, GroupName: group, Scope: firstNonEmpty(ref.Scope, "global"), bundle: &bid})
		}
	}
	return out, ids, nil
}

// writePlans replaces the role plans and bundles of a draft inside tx.
func writePlans(ctx context.Context, tx pgx.Tx, draftID uuid.UUID, plans []RolePlanInput, bundleIDs []uuid.UUID) error {
	if _, err := tx.Exec(ctx, `DELETE FROM role_plans WHERE draft_id = $1`, draftID); err != nil {
		return httperror.Internal(err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM draft_bundles WHERE draft_id = $1`, draftID); err != nil {
		return httperror.Internal(err)
	}
	for _, r := range plans {
		if _, err := tx.Exec(ctx, `
INSERT INTO role_plans (id, draft_id, role_name, group_name, scope, bundle_id) VALUES ($1, $2, $3, NULLIF($4, ''), $5, $6)`,
			uuid.New(), draftID, r.RoleName, r.GroupName, r.Scope, r.bundle); err != nil {
			return httperror.Internal(err)
		}
	}
	for _, b := range bundleIDs {
		if _, err := tx.Exec(ctx, `INSERT INTO draft_bundles (draft_id, bundle_id) VALUES ($1, $2)`, draftID, b); err != nil {
			return httperror.Internal(err)
		}
	}
	_, err := tx.Exec(ctx, `UPDATE employment_drafts SET updated_at = now() WHERE id = $1`, draftID)
	return err
}

// individualRoles are the roles of a plan that were picked one by one.
func individualRoles(plans []domain.RolePlan) []RolePlanInput {
	out := []RolePlanInput{}
	for _, r := range plans {
		if r.BundleID == nil {
			out = append(out, RolePlanInput{RoleName: r.RoleName, GroupName: r.GroupName, Scope: r.Scope})
		}
	}
	return out
}

func roleNames(refs []domain.RoleRef) []string {
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		out = append(out, r.RoleName)
	}
	return out
}

func diffRoleNames(prev, next []domain.RoleRef) (added, removed []string) {
	had, has := map[string]bool{}, map[string]bool{}
	for _, r := range prev {
		had[r.RoleName] = true
	}
	for _, r := range next {
		has[r.RoleName] = true
		if !had[r.RoleName] {
			added = append(added, r.RoleName)
		}
	}
	for _, r := range prev {
		if !has[r.RoleName] {
			removed = append(removed, r.RoleName)
		}
	}
	return added, removed
}

func uniqueIDs(in []uuid.UUID) []uuid.UUID {
	seen := map[uuid.UUID]bool{}
	out := []uuid.UUID{}
	for _, id := range in {
		if id != uuid.Nil && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}
