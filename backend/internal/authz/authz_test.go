package authz

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/AGlElectronics/hr/backend/internal/actor"
)

func TestRolePermissions(t *testing.T) {
	dept := uuid.New()
	admin := &actor.Actor{Username: "a", Roles: []string{RoleAdmin}}
	viewer := &actor.Actor{Username: "v", Roles: []string{RoleViewer}}
	lead := &actor.Actor{Username: "l", Roles: []string{RoleLead}, LeadOf: []uuid.UUID{dept}}
	leadless := &actor.Actor{Username: "l2", Roles: []string{RoleLead}}

	for _, c := range []struct {
		name           string
		a              *actor.Actor
		p              Permission
		global, scoped bool
	}{
		{"admin offboards", admin, PeopleOffboard, true, false},
		{"admin assigns privileged roles", admin, AssignPrivileged, true, false},
		{"viewer reads", viewer, PeopleRead, true, false},
		{"viewer cannot write", viewer, PeopleWrite, false, false},
		{"viewer cannot override training", viewer, TrainingOverride, false, false},
		{"lead writes in their departments", lead, PeopleWrite, false, true},
		{"lead overrides training", lead, TrainingOverride, false, true},
		{"lead deletes requirements", lead, RequirementsDelete, false, true},
		{"lead cannot offboard", lead, PeopleOffboard, false, false},
		{"lead cannot manage den", lead, DenManage, false, false},
		{"lead cannot assign privileged roles", lead, AssignPrivileged, false, false},
		{"lead without departments has nothing", leadless, PeopleRead, false, false},
	} {
		if got := Global(c.a, c.p); got != c.global {
			t.Errorf("%s: Global = %v, want %v", c.name, got, c.global)
		}
		if got := Scoped(c.a, c.p); got != c.scoped {
			t.Errorf("%s: Scoped = %v, want %v", c.name, got, c.scoped)
		}
	}
}

func TestNotSelf(t *testing.T) {
	me := uuid.New()
	ctx := actor.With(context.Background(), &actor.Actor{Username: "me", PersonID: &me, Roles: []string{RoleAdmin}})
	if err := NotSelf(ctx, me, "access"); err == nil {
		t.Fatal("changing own access was allowed")
	}
	if err := NotSelf(ctx, uuid.New(), "access"); err != nil {
		t.Fatalf("changing someone else's access refused: %v", err)
	}
}

func TestScopeFilter(t *testing.T) {
	dept := uuid.New()
	ctx := actor.With(context.Background(), &actor.Actor{Username: "l", Roles: []string{RoleLead}, LeadOf: []uuid.UUID{dept}})
	s, err := ScopeFilter(ctx, PeopleRead)
	if err != nil || len(s) != 1 || s[0] != dept {
		t.Fatalf("lead scope = %v, %v", s, err)
	}
	ctx = actor.With(context.Background(), &actor.Actor{Username: "v", Roles: []string{RoleViewer}})
	if s, err := ScopeFilter(ctx, PeopleRead); err != nil || s != nil {
		t.Fatalf("viewer scope = %v, %v (want everything)", s, err)
	}
	if _, err := ScopeFilter(ctx, PeopleWrite); err == nil {
		t.Fatal("viewer got a write scope")
	}
}
