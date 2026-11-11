package service

import (
	"context"
	"errors"
	"sort"
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
	"github.com/AGlElectronics/hr/backend/internal/qualification"
)

// Pending grant statuses.
const (
	GrantPending    = "pending"
	GrantGranted    = "granted"
	GrantOverridden = "overridden"
	GrantRevoked    = "revoked"
)

// roleGateActor is the audit actor for changes made by the worker.
const roleGateActor = "system:role-gate"

// roleGateLockKey guards the worker so only one replica runs a cycle.
const roleGateLockKey int64 = 0x4852_0006

// HoldingsSource answers which qualifications IdP users currently hold.
type HoldingsSource interface {
	Holdings(ctx context.Context, subjects []string) (map[string][]qualification.Holding, error)
}

// RoleRequirement says a role needs a qualification before it is granted.
type RoleRequirement struct {
	ID                uuid.UUID `json:"id"`
	RoleKey           string    `json:"role_key"`
	QualificationKey  string    `json:"qualification_key"`
	QualificationName string    `json:"qualification_name,omitempty"`
	RevokeOnExpiry    bool      `json:"revoke_on_expiry"`
	CreatedAt         time.Time `json:"created_at"`
}

// RoleRequirementInput creates a requirement.
type RoleRequirementInput struct {
	RoleKey          string `json:"role_key"`
	QualificationKey string `json:"qualification_key"`
	RevokeOnExpiry   bool   `json:"revoke_on_expiry"`
}

// QualificationRef is a qualification key with its display name.
type QualificationRef struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

// PendingGrant is a gated role of one person and where it stands.
type PendingGrant struct {
	ID         uuid.UUID          `json:"id"`
	PersonID   uuid.UUID          `json:"person_id"`
	PersonName string             `json:"person_name"`
	DraftID    *uuid.UUID         `json:"draft_id,omitempty"`
	RoleKey    string             `json:"role_key"`
	GroupName  string             `json:"group_name"`
	Status     string             `json:"status"`
	Reason     string             `json:"reason,omitempty"`
	CreatedAt  time.Time          `json:"created_at"`
	ResolvedAt *time.Time         `json:"resolved_at,omitempty"`
	WaitingFor []QualificationRef `json:"waiting_for"`
}

// OverrideInput explains why HR grants a role without the training.
type OverrideInput struct {
	Reason string `json:"reason"`
}

// RoleGateService holds roles back until Learn reports the required
// qualifications, and takes them away on expiry when configured.
type RoleGateService struct {
	pool     *pgxpool.Pool
	audit    *audit.Writer
	accounts provisioners.AccountManager // nil when the IdP cannot change groups
	holdings HoldingsSource              // nil when gating is off
	quals    *qualification.Fetcher      // nil-safe; only used for names
	logger   zerolog.Logger
}

// NewRoleGateService wires a RoleGateService. holdings nil turns gating off
// (requirements can still be edited but are not enforced).
func NewRoleGateService(pool *pgxpool.Pool, aud *audit.Writer, accounts provisioners.AccountManager, holdings HoldingsSource, quals *qualification.Fetcher, logger zerolog.Logger) *RoleGateService {
	return &RoleGateService{pool: pool, audit: aud, accounts: accounts, holdings: holdings, quals: quals,
		logger: logger.With().Str("component", "role_gate").Logger()}
}

// Active reports whether requirements are enforced.
func (s *RoleGateService) Active() bool { return s != nil && s.holdings != nil }

// -----------------------------------------------------------------------------
// Pure decision logic
// -----------------------------------------------------------------------------

// gateAction is what the worker does with one gated role of one person.
type gateAction int

const (
	gateNone gateAction = iota
	gateGrant
	gateRevoke
)

// decideGate: waiting roles (pending/revoked) are granted once every
// requirement is held; granted roles (status granted, or "" for holders
// without a row) are revoked when a revoke_on_expiry requirement is no
// longer held. Overridden roles are HR's call and never change.
func decideGate(status string, reqs []RoleRequirement, held map[string]bool) gateAction {
	switch status {
	case GrantPending, GrantRevoked:
		if len(unmetRequirements(reqs, held)) == 0 {
			return gateGrant
		}
	case GrantGranted, "":
		for _, r := range reqs {
			if r.RevokeOnExpiry && !held[r.QualificationKey] {
				return gateRevoke
			}
		}
	}
	return gateNone
}

// unmetRequirements returns the qualification keys of reqs not in held.
func unmetRequirements(reqs []RoleRequirement, held map[string]bool) []string {
	var out []string
	for _, r := range reqs {
		if !held[r.QualificationKey] {
			out = append(out, r.QualificationKey)
		}
	}
	return out
}

// splitRoles separates roles that can be granted now from roles still
// waiting for training.
func splitRoles(roles []domain.RolePlan, reqs map[string][]RoleRequirement, held map[string]bool) (granted, pending []domain.RolePlan) {
	for _, r := range roles {
		if len(unmetRequirements(reqs[r.RoleName], held)) > 0 {
			pending = append(pending, r)
		} else {
			granted = append(granted, r)
		}
	}
	return granted, pending
}

// -----------------------------------------------------------------------------
// Requirements
// -----------------------------------------------------------------------------

func (s *RoleGateService) qualNames(ctx context.Context) map[string]string {
	if s.quals == nil || len(s.quals.Sources) == 0 {
		return map[string]string{}
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return qualification.Names(s.quals.FetchAll(ctx))
}

func nameOr(names map[string]string, key string) string {
	if n := names[key]; n != "" {
		return n
	}
	return key
}

func (s *RoleGateService) requirements(ctx context.Context) ([]RoleRequirement, error) {
	rows, err := s.pool.Query(ctx, `
SELECT id, role_key, qualification_key, revoke_on_expiry, created_at
  FROM role_requirements ORDER BY role_key, qualification_key`)
	if err != nil {
		return nil, httperror.Internal(err)
	}
	defer rows.Close()
	out := []RoleRequirement{}
	for rows.Next() {
		var r RoleRequirement
		if err := rows.Scan(&r.ID, &r.RoleKey, &r.QualificationKey, &r.RevokeOnExpiry, &r.CreatedAt); err != nil {
			return nil, httperror.Internal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, httperror.Internal(err)
	}
	return out, nil
}

func byRole(reqs []RoleRequirement) map[string][]RoleRequirement {
	out := map[string][]RoleRequirement{}
	for _, r := range reqs {
		out[r.RoleKey] = append(out[r.RoleKey], r)
	}
	return out
}

// ListRequirements returns every requirement with qualification names.
func (s *RoleGateService) ListRequirements(ctx context.Context) ([]RoleRequirement, error) {
	reqs, err := s.requirements(ctx)
	if err != nil {
		return nil, err
	}
	if len(reqs) > 0 {
		names := s.qualNames(ctx)
		for i := range reqs {
			reqs[i].QualificationName = nameOr(names, reqs[i].QualificationKey)
		}
	}
	return reqs, nil
}

// AddRequirement makes a role wait for a qualification.
func (s *RoleGateService) AddRequirement(ctx context.Context, actor string, in RoleRequirementInput) (*RoleRequirement, error) {
	in.RoleKey, in.QualificationKey = strings.TrimSpace(in.RoleKey), strings.TrimSpace(in.QualificationKey)
	fields := map[string]string{}
	if mod, key, ok := strings.Cut(in.RoleKey, ":"); !ok || mod == "" || key == "" {
		fields["role_key"] = "pick a role (<module>:<key>)"
	}
	if in.QualificationKey == "" {
		fields["qualification_key"] = "required"
	}
	if len(fields) > 0 {
		return nil, httperror.Validation("validation failed", fields)
	}
	r := &RoleRequirement{}
	err := s.pool.QueryRow(ctx, `
INSERT INTO role_requirements (role_key, qualification_key, revoke_on_expiry) VALUES ($1, $2, $3)
RETURNING id, role_key, qualification_key, revoke_on_expiry, created_at`,
		in.RoleKey, in.QualificationKey, in.RevokeOnExpiry).
		Scan(&r.ID, &r.RoleKey, &r.QualificationKey, &r.RevokeOnExpiry, &r.CreatedAt)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return nil, httperror.Conflict("this role already requires that qualification")
	}
	if err != nil {
		return nil, httperror.Internal(err)
	}
	r.QualificationName = nameOr(s.qualNames(ctx), r.QualificationKey)
	if err := s.audit.Record(ctx, actor, "role_requirement.create", "role_requirement", r.ID, in); err != nil {
		return nil, err
	}
	return r, nil
}

// DeleteRequirement removes a requirement. Roles still waiting only on it
// are granted on the worker's next cycle.
func (s *RoleGateService) DeleteRequirement(ctx context.Context, actor string, id uuid.UUID) error {
	var roleKey, qualKey string
	err := s.pool.QueryRow(ctx, `DELETE FROM role_requirements WHERE id = $1 RETURNING role_key, qualification_key`, id).Scan(&roleKey, &qualKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return httperror.NotFound("role requirement")
	}
	if err != nil {
		return httperror.Internal(err)
	}
	return s.audit.Record(ctx, actor, "role_requirement.delete", "role_requirement", id,
		map[string]any{"role_key": roleKey, "qualification_key": qualKey})
}

// -----------------------------------------------------------------------------
// Pending grants
// -----------------------------------------------------------------------------

const pendingGrantCols = `
SELECT g.id, g.person_id, COALESCE(p.display_name, ''), g.draft_id, g.role_key, g.group_name, g.status,
       g.reason, g.created_at, g.resolved_at
  FROM pending_role_grants g JOIN people p ON p.id = g.person_id`

func (s *RoleGateService) queryGrants(ctx context.Context, where string, args ...any) ([]PendingGrant, error) {
	rows, err := s.pool.Query(ctx, pendingGrantCols+" "+where+" ORDER BY g.created_at DESC", args...)
	if err != nil {
		return nil, httperror.Internal(err)
	}
	defer rows.Close()
	out := []PendingGrant{}
	for rows.Next() {
		var g PendingGrant
		if err := rows.Scan(&g.ID, &g.PersonID, &g.PersonName, &g.DraftID, &g.RoleKey, &g.GroupName, &g.Status,
			&g.Reason, &g.CreatedAt, &g.ResolvedAt); err != nil {
			return nil, httperror.Internal(err)
		}
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
		return nil, httperror.Internal(err)
	}
	return out, nil
}

// withWaitingFor fills in the qualifications each waiting grant needs.
func (s *RoleGateService) withWaitingFor(ctx context.Context, grants []PendingGrant) ([]PendingGrant, error) {
	if len(grants) == 0 {
		return grants, nil
	}
	reqs, err := s.requirements(ctx)
	if err != nil {
		return nil, err
	}
	rr := byRole(reqs)
	var names map[string]string
	for i := range grants {
		grants[i].WaitingFor = []QualificationRef{}
		if grants[i].Status != GrantPending && grants[i].Status != GrantRevoked {
			continue
		}
		for _, r := range rr[grants[i].RoleKey] {
			if names == nil {
				names = s.qualNames(ctx)
			}
			grants[i].WaitingFor = append(grants[i].WaitingFor, QualificationRef{Key: r.QualificationKey, Name: nameOr(names, r.QualificationKey)})
		}
	}
	return grants, nil
}

// ListGrants returns gated roles, optionally filtered by status. depts, when
// not nil, limits them to people in these departments (department leads).
func (s *RoleGateService) ListGrants(ctx context.Context, status string, depts []uuid.UUID) ([]PendingGrant, error) {
	switch status = strings.TrimSpace(status); status {
	case "", GrantPending, GrantGranted, GrantOverridden, GrantRevoked:
	default:
		return nil, httperror.Validation("validation failed", map[string]string{"status": "pending, granted, overridden or revoked"})
	}
	scoped := depts != nil
	if depts == nil {
		depts = []uuid.UUID{}
	}
	grants, err := s.queryGrants(ctx, `
WHERE ($1 = '' OR g.status = $1)
  AND (NOT $2 OR EXISTS (SELECT 1 FROM employment_drafts d WHERE d.person_id = g.person_id
                          AND d.department_id = ANY($3) AND d.state <> 'rejected'))`, status, scoped, depts)
	if err != nil {
		return nil, err
	}
	return s.withWaitingFor(ctx, grants)
}

// WaitingForPerson returns the person's roles still waiting for training.
func (s *RoleGateService) WaitingForPerson(ctx context.Context, personID uuid.UUID) ([]PendingGrant, error) {
	grants, err := s.queryGrants(ctx, "WHERE g.person_id = $1 AND g.status IN ('pending', 'revoked')", personID)
	if err != nil {
		return nil, err
	}
	return s.withWaitingFor(ctx, grants)
}

// GrantPerson returns the person a pending grant belongs to.
func (s *RoleGateService) GrantPerson(ctx context.Context, id uuid.UUID) (uuid.UUID, error) {
	var person uuid.UUID
	err := s.pool.QueryRow(ctx, `SELECT person_id FROM pending_role_grants WHERE id = $1`, id).Scan(&person)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, httperror.NotFound("pending grant")
	}
	if err != nil {
		return uuid.Nil, httperror.Internal(err)
	}
	return person, nil
}

// Override grants a waiting role now, without the training.
func (s *RoleGateService) Override(ctx context.Context, actor string, id uuid.UUID, in OverrideInput) (*PendingGrant, error) {
	in.Reason = strings.TrimSpace(in.Reason)
	if in.Reason == "" {
		return nil, httperror.Validation("validation failed", map[string]string{"reason": "required"})
	}
	grants, err := s.queryGrants(ctx, "WHERE g.id = $1", id)
	if err != nil {
		return nil, err
	}
	if len(grants) == 0 {
		return nil, httperror.NotFound("pending grant")
	}
	g := grants[0]
	if g.Status != GrantPending && g.Status != GrantRevoked {
		return nil, httperror.Conflict("this role is already " + g.Status)
	}
	ext, err := latestExternalID(ctx, s.pool, g.PersonID)
	if err != nil {
		return nil, err
	}
	if ext == "" {
		return nil, httperror.Conflict("this person has no account yet, so the role cannot be granted")
	}
	if g.GroupName != "" && s.accounts != nil {
		if err := s.accounts.UpdateMembership(ctx, ext, []string{g.GroupName}, nil); err != nil {
			if errors.Is(err, provisioners.ErrAccountNotFound) {
				return nil, httperror.Conflict("this person's Keycloak account no longer exists")
			}
			return nil, idpFailure("add them to the group", err)
		}
	}
	if _, err := s.pool.Exec(ctx, `
UPDATE pending_role_grants SET status = 'overridden', reason = $2, resolved_at = now() WHERE id = $1`, id, in.Reason); err != nil {
		return nil, httperror.Internal(err)
	}
	if err := s.audit.Record(ctx, actor, "role.grant_overridden", "person", g.PersonID, map[string]any{
		"grant_id": id, "role_key": g.RoleKey, "group": g.GroupName, "reason": in.Reason,
	}); err != nil {
		return nil, err
	}
	grants, err = s.queryGrants(ctx, "WHERE g.id = $1", id)
	if err != nil || len(grants) == 0 {
		return nil, err
	}
	out, err := s.withWaitingFor(ctx, grants)
	if err != nil {
		return nil, err
	}
	return &out[0], nil
}

// -----------------------------------------------------------------------------
// Gating at onboarding / access changes
// -----------------------------------------------------------------------------

// Split separates roles that can be granted now from roles whose
// requirements are not met yet. ext is the person's IdP id ("" when they
// have no account, in which case every gated role waits). When gating is
// off or Learn cannot be asked, holdings count as empty.
func (s *RoleGateService) Split(ctx context.Context, ext string, roles []domain.RolePlan) (granted, pending []domain.RolePlan, err error) {
	if !s.Active() || len(roles) == 0 {
		return roles, nil, nil
	}
	reqs, err := s.requirements(ctx)
	if err != nil {
		return nil, nil, err
	}
	rr := byRole(reqs)
	gated := false
	for _, r := range roles {
		if len(rr[r.RoleName]) > 0 {
			gated = true
			break
		}
	}
	if !gated {
		return roles, nil, nil
	}
	held := map[string]bool{}
	if ext != "" {
		hs, err := s.holdings.Holdings(ctx, []string{ext})
		if err != nil {
			s.logger.Warn().Err(err).Msg("role gate: holdings unavailable; gated roles wait")
		} else {
			held = qualification.HeldKeys(hs[ext])
		}
	}
	granted, pending = splitRoles(roles, rr, held)
	return granted, pending, nil
}

// RecordPending stores roles as waiting for training (re-opening earlier rows).
func (s *RoleGateService) RecordPending(ctx context.Context, personID, draftID uuid.UUID, roles []domain.RolePlan) error {
	for _, r := range roles {
		if _, err := s.pool.Exec(ctx, `
INSERT INTO pending_role_grants (person_id, draft_id, role_key, group_name, status)
VALUES ($1, $2, $3, $4, 'pending')
ON CONFLICT (person_id, role_key) DO UPDATE
   SET draft_id = EXCLUDED.draft_id, group_name = EXCLUDED.group_name, status = 'pending',
       reason = '', created_at = now(), resolved_at = NULL`,
			personID, draftID, r.RoleName, r.GroupName); err != nil {
			return httperror.Internal(err)
		}
	}
	return nil
}

// waitingRoles returns the role keys of the person that are still waiting.
func (s *RoleGateService) waitingRoles(ctx context.Context, personID uuid.UUID) (map[string]bool, error) {
	out := map[string]bool{}
	if s == nil {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT role_key FROM pending_role_grants WHERE person_id = $1 AND status IN ('pending', 'revoked')`, personID)
	if err != nil {
		return nil, httperror.Internal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, httperror.Internal(err)
		}
		out[k] = true
	}
	return out, rows.Err()
}

// forgetWaiting drops waiting rows for roles the person no longer has.
func (s *RoleGateService) forgetWaiting(ctx context.Context, personID uuid.UUID, roleKeys []string) error {
	if s == nil || len(roleKeys) == 0 {
		return nil
	}
	_, err := s.pool.Exec(ctx, `
DELETE FROM pending_role_grants WHERE person_id = $1 AND role_key = ANY($2) AND status IN ('pending', 'revoked')`, personID, roleKeys)
	return err
}

// -----------------------------------------------------------------------------
// Worker
// -----------------------------------------------------------------------------

// Run re-checks gated roles every interval until ctx ends.
func (s *RoleGateService) Run(ctx context.Context, interval time.Duration) {
	if !s.Active() {
		return
	}
	if interval <= 0 {
		interval = time.Minute
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if err := s.runLocked(ctx); err != nil && ctx.Err() == nil {
			s.logger.Warn().Err(err).Msg("role gate: cycle failed")
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// runLocked runs one cycle unless another replica holds the advisory lock.
func (s *RoleGateService) runLocked(ctx context.Context) (err error) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error().Interface("panic", r).Msg("role gate: cycle panicked")
		}
	}()
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	var locked bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, roleGateLockKey).Scan(&locked); err != nil {
		return err
	}
	if !locked {
		return nil
	}
	defer func() {
		_, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, roleGateLockKey)
	}()
	return s.RunOnce(ctx)
}

// gateCandidate is one person + gated role the worker looks at.
type gateCandidate struct {
	personID uuid.UUID
	draftID  *uuid.UUID
	roleKey  string
	group    string
	status   string // "" = holder without a grant row
}

// RunOnce grants waiting roles whose requirements are now held and revokes
// revoke_on_expiry roles whose qualification lapsed. Nothing changes when
// Learn cannot be asked.
func (s *RoleGateService) RunOnce(ctx context.Context) error {
	if !s.Active() {
		return nil
	}
	reqs, err := s.requirements(ctx)
	if err != nil {
		return err
	}
	rr := byRole(reqs)
	cands, err := s.candidates(ctx, rr)
	if err != nil || len(cands) == 0 {
		return err
	}
	persons := make([]uuid.UUID, 0, len(cands))
	seen := map[uuid.UUID]bool{}
	for _, c := range cands {
		if !seen[c.personID] {
			seen[c.personID] = true
			persons = append(persons, c.personID)
		}
	}
	exts, err := s.externalIDs(ctx, persons)
	if err != nil {
		return err
	}
	subjects := make([]string, 0, len(exts))
	for _, e := range exts {
		subjects = append(subjects, e)
	}
	sort.Strings(subjects)
	holdings, err := s.holdings.Holdings(ctx, subjects)
	if err != nil {
		return err
	}
	for _, c := range cands {
		ext := exts[c.personID]
		if ext == "" {
			continue
		}
		switch decideGate(c.status, rr[c.roleKey], qualification.HeldKeys(holdings[ext])) {
		case gateGrant:
			s.apply(ctx, c, ext, GrantGranted)
		case gateRevoke:
			s.apply(ctx, c, ext, GrantRevoked)
		}
	}
	return nil
}

func (s *RoleGateService) apply(ctx context.Context, c gateCandidate, ext, to string) {
	log := s.logger.With().Str("person_id", c.personID.String()).Str("role", c.roleKey).Str("to", to).Logger()
	if s.accounts == nil {
		log.Warn().Msg("role gate: identity provider cannot change groups; skipped")
		return
	}
	if c.group != "" {
		var add, remove []string
		if to == GrantGranted {
			add = []string{c.group}
		} else {
			needed, err := s.groupStillNeeded(ctx, c.personID, c.roleKey, c.group)
			if err != nil {
				log.Warn().Err(err).Msg("role gate: group check failed")
				return
			}
			if !needed {
				remove = []string{c.group}
			}
		}
		if len(add)+len(remove) > 0 {
			if err := s.accounts.UpdateMembership(ctx, ext, add, remove); err != nil {
				log.Warn().Err(err).Msg("role gate: group change failed")
				return
			}
		}
	}
	if _, err := s.pool.Exec(ctx, `
INSERT INTO pending_role_grants (person_id, draft_id, role_key, group_name, status, resolved_at)
VALUES ($1, $2, $3, $4, $5, now())
ON CONFLICT (person_id, role_key) DO UPDATE SET status = EXCLUDED.status, resolved_at = now()`,
		c.personID, c.draftID, c.roleKey, c.group, to); err != nil {
		log.Warn().Err(err).Msg("role gate: status update failed")
		return
	}
	action := "role.granted_by_training"
	if to == GrantRevoked {
		action = "role.revoked_expired"
	}
	_ = s.audit.Record(ctx, roleGateActor, action, "person", c.personID, map[string]any{"role_key": c.roleKey, "group": c.group})
	log.Info().Msg("role gate: " + action)
}

// candidates are waiting grants plus current holders of revoke_on_expiry roles.
func (s *RoleGateService) candidates(ctx context.Context, rr map[string][]RoleRequirement) ([]gateCandidate, error) {
	out := []gateCandidate{}
	rows, err := s.pool.Query(ctx, `
SELECT person_id, draft_id, role_key, group_name, status FROM pending_role_grants
 WHERE status IN ('pending', 'revoked')`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var c gateCandidate
		if err := rows.Scan(&c.personID, &c.draftID, &c.roleKey, &c.group, &c.status); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var revocable []string
	for role, list := range rr {
		for _, r := range list {
			if r.RevokeOnExpiry {
				revocable = append(revocable, role)
				break
			}
		}
	}
	if len(revocable) == 0 {
		return out, nil
	}
	rows, err = s.pool.Query(ctx, `
SELECT DISTINCT ON (d.person_id, rp.role_name) d.person_id, d.id, rp.role_name, COALESCE(rp.group_name, ''), COALESCE(g.status, '')
  FROM role_plans rp
  JOIN employment_drafts d ON d.id = rp.draft_id AND d.state = 'active'
  JOIN people p ON p.id = d.person_id AND p.status IN ('active', 'paused')
  LEFT JOIN pending_role_grants g ON g.person_id = d.person_id AND g.role_key = rp.role_name
 WHERE rp.role_name = ANY($1) AND COALESCE(g.status, 'granted') = 'granted'
 ORDER BY d.person_id, rp.role_name, d.updated_at DESC`, revocable)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var c gateCandidate
		var draftID uuid.UUID
		if err := rows.Scan(&c.personID, &draftID, &c.roleKey, &c.group, &c.status); err != nil {
			return nil, err
		}
		c.draftID = &draftID
		out = append(out, c)
	}
	return out, rows.Err()
}

// externalIDs is latestExternalID for many people at once.
func (s *RoleGateService) externalIDs(ctx context.Context, persons []uuid.UUID) (map[uuid.UUID]string, error) {
	rows, err := s.pool.Query(ctx, `
SELECT id, idp_user_id FROM people WHERE id = ANY($1) AND idp_user_id IS NOT NULL`, persons)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uuid.UUID]string{}
	for rows.Next() {
		var id uuid.UUID
		var ext string
		if err := rows.Scan(&id, &ext); err != nil {
			return nil, err
		}
		out[id] = ext
	}
	return out, rows.Err()
}

// groupStillNeeded reports whether the person keeps the group through a
// department or another role that is not waiting.
func (s *RoleGateService) groupStillNeeded(ctx context.Context, personID uuid.UUID, roleKey, group string) (bool, error) {
	var needed bool
	err := s.pool.QueryRow(ctx, `
SELECT EXISTS (
  SELECT 1 FROM employment_drafts d JOIN departments dep ON dep.id = d.department_id
   WHERE d.person_id = $1 AND d.state = 'active' AND dep.group_name = $3
) OR EXISTS (
  SELECT 1 FROM role_plans rp JOIN employment_drafts d ON d.id = rp.draft_id AND d.state = 'active'
   WHERE d.person_id = $1 AND rp.group_name = $3 AND rp.role_name <> $2
     AND NOT EXISTS (SELECT 1 FROM pending_role_grants g
                      WHERE g.person_id = $1 AND g.role_key = rp.role_name AND g.status IN ('pending', 'revoked'))
)`, personID, roleKey, group).Scan(&needed)
	return needed, err
}
