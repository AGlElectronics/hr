// Package authz holds HR's own permission model.
//
// Fine-grained permissions are bundled into three published roles, granted
// by IdP groups (HR_ADMIN_GROUPS, HR_VIEWER_GROUPS, HR_LEAD_GROUPS):
//
//	admin   every permission, everywhere
//	viewer  read-only, everywhere
//	lead    day-to-day work, but only for people and onboardings in the
//	        departments whose lead list includes the caller
//
// Routes check a permission with Require; handlers that act on one person,
// onboarding or department then check the scope with a Checker.
package authz

import (
	"context"
	"errors"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AGlElectronics/hr/backend/internal/actor"
	"github.com/AGlElectronics/hr/backend/internal/groupmatch"
	"github.com/AGlElectronics/hr/backend/internal/httperror"
)

// Permission is one action inside HR.
type Permission string

const (
	PeopleRead         Permission = "people.read"
	PeopleCreate       Permission = "people.create"
	PeopleWrite        Permission = "people.write"
	PeoplePause        Permission = "people.pause"
	PeopleOffboard     Permission = "people.offboard"
	AccessManage       Permission = "access.manage"
	AssignPrivileged   Permission = "access.assign_privileged"
	OnboardingManage   Permission = "onboarding.manage"
	DepartmentsManage  Permission = "departments.manage"
	SettingsManage     Permission = "settings.manage"
	BundlesManage      Permission = "bundles.manage"
	DenManage          Permission = "den.manage"
	RequirementsManage Permission = "requirements.manage"
	RequirementsDelete Permission = "requirements.delete"
	TrainingOverride   Permission = "training.override"
	AuditRead          Permission = "audit.read"
	ReviewManage       Permission = "review.manage"
	ImportRun          Permission = "import.run"
	IdPGroupsRead      Permission = "idp.groups.read"
	WorkspaceManage    Permission = "workspace.manage"
)

// HR roles.
const (
	RoleAdmin  = "admin"
	RoleViewer = "viewer"
	RoleLead   = "lead"
)

var allPermissions = []Permission{
	PeopleRead, PeopleCreate, PeopleWrite, PeoplePause, PeopleOffboard, AccessManage, AssignPrivileged,
	OnboardingManage, DepartmentsManage, SettingsManage, BundlesManage, DenManage, RequirementsManage,
	RequirementsDelete, TrainingOverride, AuditRead, ReviewManage, ImportRun, IdPGroupsRead, WorkspaceManage,
}

// global permissions apply to everything; scoped ones only inside the
// departments the caller leads.
var (
	globalPerms = map[string][]Permission{
		RoleAdmin:  allPermissions,
		RoleViewer: {PeopleRead, AuditRead, IdPGroupsRead},
	}
	scopedPerms = map[string][]Permission{
		RoleLead: {PeopleRead, PeopleWrite, PeoplePause, AccessManage, OnboardingManage, TrainingOverride, ReviewManage, RequirementsDelete},
	}
)

// Global reports whether a holds p everywhere.
func Global(a *actor.Actor, p Permission) bool {
	return a != nil && holds(globalPerms, a, p)
}

// Scoped reports whether a holds p for the departments they lead.
func Scoped(a *actor.Actor, p Permission) bool {
	return a != nil && len(a.LeadOf) > 0 && holds(scopedPerms, a, p)
}

// Any reports whether a holds p anywhere.
func Any(a *actor.Actor, p Permission) bool { return Global(a, p) || Scoped(a, p) }

func holds(m map[string][]Permission, a *actor.Actor, p Permission) bool {
	for _, role := range a.Roles {
		for _, have := range m[role] {
			if have == p {
				return true
			}
		}
	}
	return false
}

// Permissions lists everything a holds (for GET /api/me), each marked
// global or scoped.
func Permissions(a *actor.Actor) map[string]string {
	out := map[string]string{}
	for _, p := range allPermissions {
		switch {
		case Global(a, p):
			out[string(p)] = "global"
		case Scoped(a, p):
			out[string(p)] = "departments"
		}
	}
	return out
}

// PermissionsFor lists the permissions of a role (for the role catalog).
func PermissionsFor(role string) []string {
	src := globalPerms[role]
	if s, ok := scopedPerms[role]; ok {
		src = s
	}
	out := make([]string, 0, len(src))
	for _, p := range src {
		out = append(out, string(p))
	}
	sort.Strings(out)
	return out
}

// Forbidden is the error for a missing permission.
func Forbidden(p Permission) error {
	return httperror.Forbidden("you don't have the HR permission " + string(p))
}

// Checker answers scope questions that need the database.
type Checker struct{ pool *pgxpool.Pool }

// NewChecker wires a Checker.
func NewChecker(pool *pgxpool.Pool) *Checker { return &Checker{pool: pool} }

func from(ctx context.Context) *actor.Actor {
	a, _ := actor.FromContext(ctx)
	return a
}

// Require fails unless the caller holds p globally.
func (c *Checker) Require(ctx context.Context, p Permission) error {
	if Global(from(ctx), p) {
		return nil
	}
	return Forbidden(p)
}

// Person fails unless the caller holds p globally, or as a lead of a
// department the person belongs to (an onboarding or membership there).
func (c *Checker) Person(ctx context.Context, p Permission, personID uuid.UUID) error {
	a := from(ctx)
	if Global(a, p) {
		return nil
	}
	if !Scoped(a, p) {
		return Forbidden(p)
	}
	var ok bool
	err := c.pool.QueryRow(ctx, `
SELECT EXISTS (SELECT 1 FROM employment_drafts
                WHERE person_id = $1 AND department_id = ANY($2) AND state <> 'rejected')`,
		personID, a.LeadOf).Scan(&ok)
	if err != nil {
		return httperror.Internal(err)
	}
	if !ok {
		return httperror.Forbidden("this person is not in a department you lead")
	}
	return nil
}

// Draft fails unless the caller holds p globally or leads the onboarding's
// department.
func (c *Checker) Draft(ctx context.Context, p Permission, draftID uuid.UUID) error {
	a := from(ctx)
	if Global(a, p) {
		return nil
	}
	if !Scoped(a, p) {
		return Forbidden(p)
	}
	var dept *uuid.UUID
	err := c.pool.QueryRow(ctx, `SELECT department_id FROM employment_drafts WHERE id = $1`, draftID).Scan(&dept)
	if errors.Is(err, pgx.ErrNoRows) {
		return httperror.NotFound("employment_draft")
	}
	if err != nil {
		return httperror.Internal(err)
	}
	if dept == nil || !a.Leads(*dept) {
		return httperror.Forbidden("this onboarding is not in a department you lead")
	}
	return nil
}

// Department fails unless the caller holds p globally or leads dept.
func (c *Checker) Department(ctx context.Context, p Permission, dept uuid.UUID) error {
	a := from(ctx)
	if Global(a, p) || (Scoped(a, p) && a.Leads(dept)) {
		return nil
	}
	if Scoped(a, p) {
		return httperror.Forbidden("you don't lead this department")
	}
	return Forbidden(p)
}

// NotSelf refuses when the caller is the person: nobody changes their own
// access, status or profile-critical fields.
func NotSelf(ctx context.Context, personID uuid.UUID, what string) error {
	if a := from(ctx); a != nil && a.PersonID != nil && *a.PersonID == personID {
		return httperror.Forbidden("you can't change your own " + what + "; ask another HR admin")
	}
	return nil
}

// ScopeFilter returns nil when the caller reads everything, or the
// departments a lead may read.
func ScopeFilter(ctx context.Context, p Permission) ([]uuid.UUID, error) {
	a := from(ctx)
	switch {
	case Global(a, p):
		return nil, nil
	case Scoped(a, p):
		return a.LeadOf, nil
	default:
		return nil, Forbidden(p)
	}
}

// Resolver finds the caller's HR person record and the departments they
// lead. Middleware calls it once per request.
type Resolver struct {
	pool   *pgxpool.Pool
	groups groupmatch.Config
}

// NewResolver wires a Resolver; a nil pool resolves nothing. groups is how
// token groups are compared with department lead groups.
func NewResolver(pool *pgxpool.Pool, groups groupmatch.Config) *Resolver {
	return &Resolver{pool: pool, groups: groups}
}

// Resolve fills a.PersonID and a.LeadOf. A caller leads a department when
// HR's lead list names their person record, or when their token carries the
// department's lead group (dept-<slug>-lead), matched with groupmatch.
func (r *Resolver) Resolve(ctx context.Context, a *actor.Actor) error {
	if r == nil || r.pool == nil || a.Subject == "" {
		return nil
	}
	var id uuid.UUID
	err := r.pool.QueryRow(ctx, `
SELECT id FROM people WHERE idp_subject = $1 OR (idp_subject IS NULL AND idp_user_id = $1) LIMIT 1`, a.Subject).Scan(&id)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return err
	default:
		a.PersonID = &id
	}
	rows, err := r.pool.Query(ctx, `
SELECT d.id, d.group_name, ($1::uuid IS NOT NULL AND EXISTS (
         SELECT 1 FROM department_leads dl WHERE dl.department_id = d.id AND dl.person_id = $1))
  FROM departments d WHERE d.archived_at IS NULL`, a.PersonID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var d uuid.UUID
		var group string
		var listed bool
		if err := rows.Scan(&d, &group, &listed); err != nil {
			return err
		}
		if listed || (len(a.Groups) > 0 && r.groups.Match(a.Groups, group+"-lead")) {
			a.LeadOf = append(a.LeadOf, d)
		}
	}
	return rows.Err()
}
