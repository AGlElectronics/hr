package service

import (
	"context"
	"sort"
	"strings"

	"github.com/AGlElectronics/hr/backend/internal/actor"
	"github.com/AGlElectronics/hr/backend/internal/authz"
	"github.com/AGlElectronics/hr/backend/internal/catalog"
	"github.com/AGlElectronics/hr/backend/internal/domain"
	"github.com/AGlElectronics/hr/backend/internal/httperror"
)

// AccessValidator checks role assignments against the module catalogs: HR
// only assigns roles a module publishes (live or from the last cached
// catalog), always with the catalog's group, and admin-type roles only for
// callers with the "assign admin roles" permission. HR never invents groups.
type AccessValidator struct {
	catalogs func(ctx context.Context) []catalog.Module
}

// NewAccessValidator wires an AccessValidator.
func NewAccessValidator(catalogs func(ctx context.Context) []catalog.Module) *AccessValidator {
	return &AccessValidator{catalogs: catalogs}
}

// Index returns the current catalog index.
func (v *AccessValidator) Index(ctx context.Context) *catalog.Index {
	if v == nil || v.catalogs == nil {
		return catalog.NewIndex(nil)
	}
	return catalog.NewIndex(v.catalogs(ctx))
}

// ValidateRoles checks requested roles. Roles in keep (already assigned)
// pass unchanged, even when their module retired them, so HR can keep or
// remove them.
func (v *AccessValidator) ValidateRoles(ctx context.Context, roles []RolePlanInput, keep []domain.RolePlan) ([]RolePlanInput, error) {
	return v.validate(ctx, roles, keep, false)
}

// ValidateDefaults checks roles an HR admin configured as defaults
// (department defaults, access groups). They need not pass the privilege
// check again when a lead applies them.
func (v *AccessValidator) ValidateDefaults(ctx context.Context, roles []RolePlanInput) ([]RolePlanInput, error) {
	return v.validate(ctx, roles, nil, true)
}

func (v *AccessValidator) validate(ctx context.Context, roles []RolePlanInput, keep []domain.RolePlan, trusted bool) ([]RolePlanInput, error) {
	if v == nil {
		return roles, nil
	}
	ix := v.Index(ctx)
	kept := map[string]domain.RolePlan{}
	for _, r := range keep {
		kept[r.RoleName] = r
	}
	a, _ := actor.FromContext(ctx)
	mayPrivileged := trusted || authz.Global(a, authz.AssignPrivileged)
	issues := map[string]string{}
	out := make([]RolePlanInput, 0, len(roles))
	for _, r := range roles {
		r.RoleName = strings.TrimSpace(r.RoleName)
		if k, ok := kept[r.RoleName]; ok {
			r.GroupName, r.Scope = k.GroupName, firstNonEmpty(k.Scope, "global")
			out = append(out, r)
			continue
		}
		role, state := ix.Lookup(r.RoleName)
		field := "roles." + r.RoleName
		if trusted && (state != catalog.RoleKnown || !role.Assignable || role.Group == "") {
			// A configured default the module no longer offers is dropped
			// instead of blocking every onboarding.
			continue
		}
		switch {
		case state == catalog.RoleRetired:
			issues[field] = "this role is no longer offered by its module"
		case state == catalog.RoleUnknownModule:
			issues[field] = "unknown role: its module is not connected or has never published a catalog"
		case !role.Assignable:
			issues[field] = "this role cannot be assigned by HR"
		case role.Group == "":
			issues[field] = "this role has no group to assign"
		case role.AdminType() && !mayPrivileged:
			issues[field] = "assigning admin roles needs the HR permission " + string(authz.AssignPrivileged)
		default:
			r.GroupName = role.Group
			if r.Scope == "" {
				r.Scope = "global"
			}
			out = append(out, r)
		}
	}
	if len(issues) > 0 {
		return nil, httperror.Validation("some roles cannot be assigned", issues)
	}
	return out, nil
}

// Annotate marks roles their module no longer publishes as retired.
func (v *AccessValidator) Annotate(ctx context.Context, plans []domain.RolePlan) {
	if v == nil || len(plans) == 0 {
		return
	}
	ix := v.Index(ctx)
	for i := range plans {
		_, state := ix.Lookup(plans[i].RoleName)
		plans[i].Retired = state == catalog.RoleRetired
	}
}

// KnownGroups lists every group HR may assign from the catalogs.
func (v *AccessValidator) KnownGroups(ctx context.Context) []string {
	out := []string{}
	for g := range v.Index(ctx).Groups() {
		out = append(out, g)
	}
	sort.Strings(out)
	return out
}
