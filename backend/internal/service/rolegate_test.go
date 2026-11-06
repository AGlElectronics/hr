package service

import (
	"testing"

	"github.com/AGlElectronics/hr/backend/internal/domain"
)

func TestDecideGate(t *testing.T) {
	firstAid := RoleRequirement{RoleKey: "stash:lead", QualificationKey: "first-aid"}
	forklift := RoleRequirement{RoleKey: "stash:lead", QualificationKey: "forklift", RevokeOnExpiry: true}
	both := []RoleRequirement{firstAid, forklift}
	held := func(keys ...string) map[string]bool {
		m := map[string]bool{}
		for _, k := range keys {
			m[k] = true
		}
		return m
	}

	cases := []struct {
		name   string
		status string
		reqs   []RoleRequirement
		held   map[string]bool
		want   gateAction
	}{
		{"pending, nothing held", GrantPending, both, held(), gateNone},
		{"pending, one of two held", GrantPending, both, held("first-aid"), gateNone},
		{"pending, all held", GrantPending, both, held("first-aid", "forklift", "other"), gateGrant},
		{"pending, requirement deleted", GrantPending, nil, held(), gateGrant},
		{"revoked, renewed", GrantRevoked, both, held("first-aid", "forklift"), gateGrant},
		{"granted, all still held", GrantGranted, both, held("first-aid", "forklift"), gateNone},
		{"granted, non-revoking qualification lapsed", GrantGranted, both, held("forklift"), gateNone},
		{"granted, revoking qualification lapsed", GrantGranted, both, held("first-aid"), gateRevoke},
		{"holder without row, revoking lapsed", "", both, held("first-aid"), gateRevoke},
		{"overridden never changes", GrantOverridden, both, held(), gateNone},
		{"overridden with everything held", GrantOverridden, both, held("first-aid", "forklift"), gateNone},
	}
	for _, c := range cases {
		if got := decideGate(c.status, c.reqs, c.held); got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}
}

func TestSplitRoles(t *testing.T) {
	roles := []domain.RolePlan{
		{RoleName: "stash:volunteer", GroupName: "stash-volunteer"},
		{RoleName: "stash:lead", GroupName: "stash-lead"},
		{RoleName: "events:medic", GroupName: "events-medic"},
	}
	reqs := byRole([]RoleRequirement{
		{RoleKey: "stash:lead", QualificationKey: "first-aid"},
		{RoleKey: "events:medic", QualificationKey: "first-aid"},
		{RoleKey: "events:medic", QualificationKey: "medic"},
	})

	granted, pending := splitRoles(roles, reqs, map[string]bool{})
	if len(granted) != 1 || granted[0].RoleName != "stash:volunteer" || len(pending) != 2 {
		t.Fatalf("no account: granted %v pending %v", granted, pending)
	}

	granted, pending = splitRoles(roles, reqs, map[string]bool{"first-aid": true})
	if len(granted) != 2 || len(pending) != 1 || pending[0].RoleName != "events:medic" {
		t.Fatalf("first aid held: granted %v pending %v", granted, pending)
	}

	if unmet := unmetRequirements(reqs["events:medic"], map[string]bool{"first-aid": true}); len(unmet) != 1 || unmet[0] != "medic" {
		t.Fatalf("unmet %v", unmet)
	}
}

func TestRoleGateInactive(t *testing.T) {
	var g *RoleGateService
	if g.Active() {
		t.Fatal("nil gate must be inactive")
	}
	roles := []domain.RolePlan{{RoleName: "stash:lead"}}
	granted, pending, err := g.Split(t.Context(), "", roles)
	if err != nil || len(granted) != 1 || len(pending) != 0 {
		t.Fatalf("inactive gate must grant everything: %v %v %v", granted, pending, err)
	}
	if (&RoleGateService{}).Active() {
		t.Fatal("gate without holdings source must be inactive")
	}
}
