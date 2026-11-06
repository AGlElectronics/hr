package http_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/AGlElectronics/hr/backend/internal/config"
	hrhttp "github.com/AGlElectronics/hr/backend/internal/http"
	"github.com/AGlElectronics/hr/backend/internal/provisioners"
	"github.com/AGlElectronics/hr/backend/internal/service"
)

// fakeLearn serves the qualification catalog and holdings contract.
type fakeLearn struct {
	mu       sync.Mutex
	holdings map[string][]string
}

func (f *fakeLearn) set(subject string, keys ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.holdings[subject] = keys
}

func (f *fakeLearn) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/api/qualification-catalog":
		_, _ = w.Write([]byte(`{"module":"learn","display_name":"Learn","qualifications":[{"key":"first-aid","name":"First aid","valid_months":24}]}`))
	case "/api/internal/holdings":
		if r.Header.Get("Authorization") != "Bearer learn-test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		f.mu.Lock()
		out := map[string][]map[string]any{}
		for _, s := range strings.Split(r.URL.Query().Get("subjects"), ",") {
			for _, k := range f.holdings[s] {
				out[s] = append(out[s], map[string]any{"key": k, "expires_at": nil})
			}
		}
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"holdings": out})
	default:
		http.NotFound(w, r)
	}
}

// Roles with a training requirement wait at onboarding, are granted by the
// worker once Learn reports the qualification, revoked when it lapses
// (revoke_on_expiry), and can be overridden by HR with a reason.
func TestRoleGating(t *testing.T) {
	learn := &fakeLearn{holdings: map[string][]string{}}
	srv := httptest.NewServer(learn)
	defer srv.Close()

	var gate *service.RoleGateService
	var idp *provisioners.FakeIdP
	do, pool := newTestClientWith(t, func(pool *pgxpool.Pool, cfg *config.Config, deps *hrhttp.Deps) {
		cfg.QualificationCatalogSources = "learn=" + srv.URL + "/api/qualification-catalog"
		cfg.LearnHoldingsURL = srv.URL + "/api/internal/holdings"
		cfg.LearnServiceToken = "learn-test-token"
		idp = provisioners.NewFakeIdP(zerolog.Nop())
		deps.IdP = idp
		gate = hrhttp.NewRoleGate(cfg, pool, idp, zerolog.Nop())
		deps.RoleGate = gate
	})
	ctx := context.Background()

	rec := do(http.MethodGet, "/api/qualification-catalog", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"First aid"`) {
		t.Fatalf("qualification catalog %d %s", rec.Code, rec.Body.String())
	}

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	gatedRole, gatedGroup := "stash:lead"+suffix, "stash-lead-"+suffix
	rec = do(http.MethodPost, "/api/role-requirements", map[string]any{"role_key": gatedRole, "qualification_key": "first-aid", "revoke_on_expiry": true})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create requirement %d %s", rec.Code, rec.Body.String())
	}
	var req struct {
		ID   string `json:"id"`
		Name string `json:"qualification_name"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &req)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM role_requirements WHERE id = $1`, req.ID) })
	if req.Name != "First aid" {
		t.Fatalf("requirement name: %s", rec.Body.String())
	}
	if rec = do(http.MethodPost, "/api/role-requirements", map[string]any{"role_key": gatedRole, "qualification_key": "first-aid"}); rec.Code != http.StatusConflict {
		t.Fatalf("duplicate requirement: want 409, got %d", rec.Code)
	}
	if rec = do(http.MethodPost, "/api/role-requirements", map[string]any{"role_key": "nomodule", "qualification_key": "first-aid"}); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad role key: want 422, got %d", rec.Code)
	}
	rec = do(http.MethodGet, "/api/role-requirements", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), gatedRole) || !strings.Contains(rec.Body.String(), `"active":true`) {
		t.Fatalf("list requirements %d %s", rec.Code, rec.Body.String())
	}

	slug := "gate-" + suffix
	deptRec := do(http.MethodPost, "/api/departments", map[string]any{"name": "Gate " + suffix, "slug": slug})
	var dept struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(deptRec.Body.Bytes(), &dept)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM employment_drafts WHERE department_id = $1`, dept.ID)
		_, _ = pool.Exec(ctx, `DELETE FROM departments WHERE id = $1`, dept.ID)
	})
	startRec := do(http.MethodPost, "/api/onboardings", map[string]any{
		"department_id": dept.ID,
		"person":        completePerson("Florence", "Nightingale", uniqueEmail("florence"), uniqueNick("flo")),
	})
	if startRec.Code != http.StatusCreated {
		t.Fatalf("start onboarding %d %s", startRec.Code, startRec.Body.String())
	}
	var view struct {
		Draft  struct{ ID string } `json:"draft"`
		Person struct{ ID string } `json:"person"`
	}
	_ = json.Unmarshal(startRec.Body.Bytes(), &view)
	cleanupPerson(t, pool, view.Person.ID)
	if rec = do(http.MethodPut, "/api/drafts/"+view.Draft.ID+"/roles", map[string]any{"roles": []map[string]string{
		{"role_name": "stash:volunteer", "group_name": "stash-volunteer"},
		{"role_name": gatedRole, "group_name": gatedGroup},
	}}); rec.Code >= 300 {
		t.Fatalf("assign roles %d %s", rec.Code, rec.Body.String())
	}

	type grant struct {
		ID         string `json:"id"`
		RoleKey    string `json:"role_key"`
		Status     string `json:"status"`
		WaitingFor []struct {
			Name string `json:"name"`
		} `json:"waiting_for"`
	}
	rec = do(http.MethodPost, "/api/onboardings/"+view.Draft.ID+"/complete", nil)
	var res struct {
		OK            bool     `json:"ok"`
		GroupsApplied []string `json:"groups_applied"`
		PendingGrants []grant  `json:"pending_grants"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &res)
	if rec.Code != http.StatusOK || !res.OK {
		t.Fatalf("complete %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(strings.Join(res.GroupsApplied, ","), gatedGroup) || !strings.Contains(strings.Join(res.GroupsApplied, ","), "stash-volunteer") {
		t.Fatalf("gated group must wait, volunteer must be applied: %v", res.GroupsApplied)
	}
	if len(res.PendingGrants) != 1 || res.PendingGrants[0].RoleKey != gatedRole || len(res.PendingGrants[0].WaitingFor) != 1 || res.PendingGrants[0].WaitingFor[0].Name != "First aid" {
		t.Fatalf("pending grants in result: %s", rec.Body.String())
	}
	grantID := res.PendingGrants[0].ID

	rec = do(http.MethodGet, "/api/onboardings/"+view.Draft.ID, nil)
	if !strings.Contains(rec.Body.String(), `"pending_grants":[{`) || !strings.Contains(rec.Body.String(), "First aid") {
		t.Fatalf("overview pending grants: %s", rec.Body.String())
	}
	rec = do(http.MethodGet, "/api/pending-grants?status=pending", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), grantID) {
		t.Fatalf("list pending %d %s", rec.Code, rec.Body.String())
	}
	if rec = do(http.MethodGet, "/api/pending-grants?status=nope", nil); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad status filter: want 422, got %d", rec.Code)
	}

	statusOf := func() string {
		var s string
		if err := pool.QueryRow(ctx, `SELECT status FROM pending_role_grants WHERE id = $1`, grantID).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	lastChange := func() provisioners.MembershipChange {
		ms := idp.Memberships()
		if len(ms) == 0 {
			t.Fatal("no membership changes")
		}
		return ms[len(ms)-1]
	}
	ext := "idp:" + view.Person.ID

	// Not trained yet: nothing changes.
	if err := gate.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if s := statusOf(); s != "pending" {
		t.Fatalf("untrained: want pending, got %s", s)
	}

	learn.set(ext, "first-aid")
	if err := gate.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if s := statusOf(); s != "granted" {
		t.Fatalf("trained: want granted, got %s", s)
	}
	if c := lastChange(); c.UserID != ext || len(c.Add) != 1 || c.Add[0] != gatedGroup {
		t.Fatalf("grant membership change %+v", c)
	}
	var audits int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE entity_id = $1 AND action = 'role.granted_by_training'`, view.Person.ID).Scan(&audits)
	if audits != 1 {
		t.Fatalf("granted audit lines: %d", audits)
	}

	learn.set(ext)
	if err := gate.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if s := statusOf(); s != "revoked" {
		t.Fatalf("expired: want revoked, got %s", s)
	}
	if c := lastChange(); len(c.Remove) != 1 || c.Remove[0] != gatedGroup {
		t.Fatalf("revoke membership change %+v", c)
	}

	if rec = do(http.MethodPost, "/api/pending-grants/"+grantID+"/override", map[string]any{"reason": " "}); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("override without reason: want 422, got %d", rec.Code)
	}
	rec = do(http.MethodPost, "/api/pending-grants/"+grantID+"/override", map[string]any{"reason": "trained on paper"})
	var g grant
	_ = json.Unmarshal(rec.Body.Bytes(), &g)
	if rec.Code != http.StatusOK || g.Status != "overridden" {
		t.Fatalf("override %d %s", rec.Code, rec.Body.String())
	}
	if c := lastChange(); len(c.Add) != 1 || c.Add[0] != gatedGroup {
		t.Fatalf("override membership change %+v", c)
	}
	if rec = do(http.MethodPost, "/api/pending-grants/"+grantID+"/override", map[string]any{"reason": "again"}); rec.Code != http.StatusConflict {
		t.Fatalf("second override: want 409, got %d", rec.Code)
	}
	if err := gate.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if s := statusOf(); s != "overridden" {
		t.Fatalf("overridden must stay, got %s", s)
	}

	// Learn down: the worker reports the error and changes nothing.
	srv.Close()
	if _, err := pool.Exec(ctx, `UPDATE pending_role_grants SET status = 'pending' WHERE id = $1`, grantID); err != nil {
		t.Fatal(err)
	}
	if err := gate.RunOnce(ctx); err == nil {
		t.Fatal("unreachable Learn should report an error")
	}
	if s := statusOf(); s != "pending" {
		t.Fatalf("Learn down: want pending, got %s", s)
	}

	// Changing access later keeps a waiting role's group out; dropping the
	// role forgets the waiting grant.
	roles := []map[string]string{{"role_name": "stash:volunteer", "group_name": "stash-volunteer"}, {"role_name": gatedRole, "group_name": gatedGroup}}
	if rec = do(http.MethodPut, "/api/people/"+view.Person.ID+"/access", map[string]any{"draft_id": view.Draft.ID, "roles": roles}); rec.Code != http.StatusOK {
		t.Fatalf("change access %d %s", rec.Code, rec.Body.String())
	}
	if c := lastChange(); strings.Contains(strings.Join(c.Add, ","), gatedGroup) {
		t.Fatalf("waiting group added on access change: %+v", c)
	}
	if rec = do(http.MethodPut, "/api/people/"+view.Person.ID+"/access", map[string]any{"draft_id": view.Draft.ID, "roles": roles[:1]}); rec.Code != http.StatusOK {
		t.Fatalf("drop gated role %d %s", rec.Code, rec.Body.String())
	}
	var left int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM pending_role_grants WHERE id = $1`, grantID).Scan(&left)
	if left != 0 {
		t.Fatal("waiting grant should be forgotten when the role is removed")
	}
}
