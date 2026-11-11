package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/AGlElectronics/hr/backend/internal/actor"
	"github.com/AGlElectronics/hr/backend/internal/config"
)

func TestInAnyGroup(t *testing.T) {
	cases := []struct {
		have, want []string
		ok         bool
	}{
		{[]string{"/hr-admin"}, []string{"hr-admin"}, true},
		{[]string{"HR-Admin"}, []string{"hr-admin"}, true},
		{[]string{"stash-volunteer", "dept-ops"}, []string{"hr-admin"}, false},
		{nil, []string{"hr-admin"}, false},
	}
	for _, c := range cases {
		if got := inAnyGroup(c.have, c.want); got != c.ok {
			t.Errorf("inAnyGroup(%v, %v) = %v, want %v", c.have, c.want, got, c.ok)
		}
	}
}

func TestRolesFor(t *testing.T) {
	cfg := &config.Config{AdminGroups: []string{"hr-admin"}, ViewerGroups: []string{"hr-viewer"}, LeadGroups: []string{"/org/hr-lead"}}
	got := RolesFor(cfg, []string{"/hr-viewer", "org/hr-lead", "stash-volunteer"})
	if len(got) != 2 || got[0] != "viewer" || got[1] != "lead" {
		t.Fatalf("RolesFor = %v", got)
	}
	if got := RolesFor(cfg, []string{"stash-volunteer"}); len(got) != 0 {
		t.Fatalf("volunteer got HR roles %v", got)
	}
}

func TestRequireAccess(t *testing.T) {
	h := RequireAccess(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	for name, tc := range map[string]struct {
		a    *actor.Actor
		want int
	}{
		"no actor":  {nil, http.StatusForbidden},
		"volunteer": {&actor.Actor{Username: "vol", Groups: []string{"stash-volunteer"}}, http.StatusForbidden},
		"hr admin":  {&actor.Actor{Username: "hr", Groups: []string{"hr-admin"}, HasAccess: true}, http.StatusOK},
	} {
		req := httptest.NewRequest(http.MethodGet, "/api/people", nil)
		if tc.a != nil {
			req = req.WithContext(actor.With(req.Context(), tc.a))
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Errorf("%s: got %d, want %d", name, rec.Code, tc.want)
		}
	}
}
