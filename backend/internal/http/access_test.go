package http_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestRoleValidation: HR only assigns roles a module publishes, always with
// the catalog's group, and admin roles only for HR admins.
func TestRoleValidation(t *testing.T) {
	router, pool := newTestRouter(t, nil)
	admin := clientAs(router, "test-hr-admin", "")
	dept, _ := createDepartment(t, admin, pool, "Valid")
	_, draft := startOnboarding(t, admin, pool, dept, "Vera")

	rec := admin(http.MethodPut, "/api/drafts/"+draft+"/roles", map[string]any{"roles": []map[string]string{
		{"role_name": "stash:volunteer", "group_name": "den-admin"},
	}})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"group_name":"stash-volunteer"`) || strings.Contains(rec.Body.String(), "den-admin") {
		t.Fatalf("group must come from the catalog: %d %s", rec.Code, rec.Body.String())
	}
	for role, want := range map[string]string{
		"stash:nonexistent": "no longer offered",
		"other:role":        "unknown role",
		"stash:system":      "cannot be assigned",
	} {
		rec = admin(http.MethodPut, "/api/drafts/"+draft+"/roles", map[string]any{"roles": []map[string]string{{"role_name": role}}})
		if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("%s: want 422 %q, got %d %s", role, want, rec.Code, rec.Body.String())
		}
	}

	leadID, _ := startOnboarding(t, admin, pool, dept, "Lars")
	if _, err := pool.Exec(context.Background(), `UPDATE people SET idp_subject = 'validation-lead' WHERE id = $1`, leadID); err != nil {
		t.Fatal(err)
	}
	admin(http.MethodPut, "/api/departments/"+dept+"/leads", map[string]any{"person_ids": []string{leadID}})
	lead := clientAs(router, "validation-lead", "hr-lead")
	rec = lead(http.MethodPut, "/api/drafts/"+draft+"/roles", map[string]any{"roles": []map[string]string{{"role_name": "stash:admin"}}})
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "access.assign_privileged") {
		t.Fatalf("lead assigning an admin role: want 422, got %d %s", rec.Code, rec.Body.String())
	}
	if rec = admin(http.MethodPut, "/api/drafts/"+draft+"/roles", map[string]any{"roles": []map[string]string{{"role_name": "stash:admin"}}}); rec.Code != http.StatusOK {
		t.Fatalf("admin assigning an admin role: %d %s", rec.Code, rec.Body.String())
	}
}

// TestAccessBundles: an access group expands into its roles, the person
// page shows where each role came from, and editing the bundle updates
// everyone holding it in the background.
func TestAccessBundles(t *testing.T) {
	router, pool := newTestRouter(t, nil)
	admin := clientAs(router, "test-hr-admin", "")
	dept, _ := createDepartment(t, admin, pool, "Bundled")

	rec := admin(http.MethodPost, "/api/access-bundles", map[string]any{
		"name": "Standard volunteer " + uniqueNick("b"), "roles": []map[string]string{{"role_name": "stash:volunteer"}},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create bundle %d %s", rec.Code, rec.Body.String())
	}
	var bundle struct{ ID, Name string }
	_ = json.Unmarshal(rec.Body.Bytes(), &bundle)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM draft_bundles WHERE bundle_id = $1`, bundle.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM access_bundles WHERE id = $1`, bundle.ID)
	})

	person, draft := startOnboarding(t, admin, pool, dept, "Bea")
	rec = admin(http.MethodPut, "/api/drafts/"+draft+"/roles", map[string]any{
		"roles": []map[string]string{{"role_name": "stash:lead"}}, "bundle_ids": []string{bundle.ID},
	})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"bundle_name":"`+bundle.Name+`"`) {
		t.Fatalf("assign bundle %d %s", rec.Code, rec.Body.String())
	}
	completeOnboarding(t, admin, draft)

	rec = admin(http.MethodPut, "/api/access-bundles/"+bundle.ID, map[string]any{
		"name": bundle.Name, "roles": []map[string]string{{"role_name": "stash:volunteer"}, {"role_name": "stash:scanner"}},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("update bundle %d %s", rec.Code, rec.Body.String())
	}
	waitFor(t, pool, `SELECT EXISTS (SELECT 1 FROM role_plans WHERE draft_id = $1 AND role_name = 'stash:scanner' AND bundle_id IS NOT NULL)`, draft)

	var acc struct {
		Memberships []struct {
			Roles []struct {
				RoleName   string `json:"role_name"`
				BundleName string `json:"bundle_name"`
			} `json:"roles"`
		} `json:"memberships"`
	}
	_ = json.Unmarshal(admin(http.MethodGet, "/api/people/"+person+"/access", nil).Body.Bytes(), &acc)
	got := map[string]string{}
	for _, r := range acc.Memberships[0].Roles {
		got[r.RoleName] = r.BundleName
	}
	if got["stash:lead"] != "" || got["stash:volunteer"] != bundle.Name || got["stash:scanner"] != bundle.Name {
		t.Fatalf("role sources after bundle edit: %v", got)
	}
	var synced int
	_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_events WHERE entity_id = $1 AND action = 'bundle.sync'`, person).Scan(&synced)
	if synced != 1 {
		t.Fatalf("bundle sync audited %d times", synced)
	}
	if rec = admin(http.MethodDelete, "/api/access-bundles/"+bundle.ID, nil); rec.Code != http.StatusConflict {
		t.Fatalf("deleting a bundle in use: want 409, got %d", rec.Code)
	}
}

// completeOnboarding finishes an onboarding and waits until provisioning
// is done.
func completeOnboarding(t *testing.T, do testClient, draftID string) {
	t.Helper()
	rec := do(http.MethodPost, "/api/onboardings/"+draftID+"/complete", nil)
	if rec.Code >= 300 {
		t.Fatalf("complete %d %s", rec.Code, rec.Body.String())
	}
	if st := waitProvisioning(t, do, draftID); st != "succeeded" {
		t.Fatalf("provisioning %s: %s", st, do(http.MethodGet, "/api/onboardings/"+draftID, nil).Body.String())
	}
}

// waitProvisioning waits until the onboarding's completion job finished and
// returns its status (succeeded or failed).
func waitProvisioning(t *testing.T, do testClient, draftID string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var v struct {
			Provisioning struct{ Status string } `json:"provisioning"`
		}
		_ = json.Unmarshal(do(http.MethodGet, "/api/onboardings/"+draftID, nil).Body.Bytes(), &v)
		if s := v.Provisioning.Status; s == "succeeded" || s == "failed" {
			return s
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("provisioning did not finish")
	return ""
}

// waitFor polls a boolean query until it is true.
func waitFor(t *testing.T, pool *pgxpool.Pool, query string, args ...any) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var done bool
		if err := pool.QueryRow(context.Background(), query, args...).Scan(&done); err == nil && done {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", query)
}
