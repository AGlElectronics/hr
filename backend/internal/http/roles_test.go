package http_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// createDepartment adds a department and removes it (with its onboardings)
// after the test.
func createDepartment(t *testing.T, do testClient, pool *pgxpool.Pool, name string) (id, group string) {
	t.Helper()
	slug := fmt.Sprintf("%s-%d", strings.ToLower(name), time.Now().UnixNano())
	rec := do(http.MethodPost, "/api/departments", map[string]any{"name": name, "slug": slug, "default_roles": []any{}})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create department %d %s", rec.Code, rec.Body.String())
	}
	var d struct {
		ID        string `json:"id"`
		GroupName string `json:"group_name"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &d)
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM people WHERE id IN (SELECT person_id FROM employment_drafts WHERE department_id = $1)`, d.ID)
		_, _ = pool.Exec(ctx, `DELETE FROM employment_drafts WHERE department_id = $1`, d.ID)
		_, _ = pool.Exec(ctx, `DELETE FROM departments WHERE id = $1`, d.ID)
	})
	return d.ID, d.GroupName
}

// startOnboarding creates a volunteer with an onboarding in the department.
func startOnboarding(t *testing.T, do testClient, pool *pgxpool.Pool, deptID, first string) (personID, draftID string) {
	t.Helper()
	rec := do(http.MethodPost, "/api/onboardings", map[string]any{
		"department_id": deptID,
		"person":        completePerson(first, "Tester", uniqueEmail(strings.ToLower(first)), uniqueNick(strings.ToLower(first))),
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("start onboarding %d %s", rec.Code, rec.Body.String())
	}
	var v struct {
		Draft  struct{ ID string } `json:"draft"`
		Person struct{ ID string } `json:"person"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &v)
	cleanupPerson(t, pool, v.Person.ID)
	return v.Person.ID, v.Draft.ID
}

// TestRolesAndScopes checks the HR roles on the real router: a lead only
// sees and changes people in the departments they lead, a viewer only
// reads, and nobody changes their own access or critical profile fields.
func TestRolesAndScopes(t *testing.T) {
	router, pool := newTestRouter(t, nil)
	admin := clientAs(router, "test-hr-admin", "")

	deptA, _ := createDepartment(t, admin, pool, "Alpha")
	deptB, _ := createDepartment(t, admin, pool, "Beta")
	inA, draftA := startOnboarding(t, admin, pool, deptA, "Anna")
	inB, _ := startOnboarding(t, admin, pool, deptB, "Bert")
	leadID, _ := startOnboarding(t, admin, pool, deptA, "Lena")

	leadUser := uniqueNick("leaduser")
	if _, err := pool.Exec(context.Background(), `UPDATE people SET idp_subject = $2 WHERE id = $1`, leadID, leadUser); err != nil {
		t.Fatal(err)
	}
	if rec := admin(http.MethodPut, "/api/departments/"+deptA+"/leads", map[string]any{"person_ids": []string{leadID}}); rec.Code != http.StatusOK {
		t.Fatalf("set leads %d %s", rec.Code, rec.Body.String())
	}

	lead := clientAs(router, leadUser, "hr-lead")
	rec := lead(http.MethodGet, "/api/me", nil)
	if !strings.Contains(rec.Body.String(), deptA) || !strings.Contains(rec.Body.String(), `"people.write":"departments"`) {
		t.Fatalf("lead /me: %s", rec.Body.String())
	}
	rec = lead(http.MethodGet, "/api/people?limit=200", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), inA) || strings.Contains(rec.Body.String(), inB) {
		t.Fatalf("lead people list should show only department Alpha: %d %s", rec.Code, rec.Body.String())
	}
	if rec = lead(http.MethodGet, "/api/people/"+inB, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("lead reading a person outside their departments: want 403, got %d", rec.Code)
	}
	if rec = lead(http.MethodGet, "/api/onboardings/"+draftA, nil); rec.Code != http.StatusOK {
		t.Fatalf("lead reading an onboarding in their department: %d %s", rec.Code, rec.Body.String())
	}
	if rec = lead(http.MethodPost, "/api/onboardings", map[string]any{"department_id": deptB, "person": completePerson("X", "Y", uniqueEmail("x"), uniqueNick("x"))}); rec.Code != http.StatusForbidden {
		t.Fatalf("lead onboarding into another department: want 403, got %d", rec.Code)
	}
	if rec = lead(http.MethodPut, "/api/departments/"+deptA, map[string]any{"name": "Renamed", "default_roles": []any{}}); rec.Code != http.StatusForbidden {
		t.Fatalf("lead editing a department: want 403, got %d", rec.Code)
	}
	if rec = lead(http.MethodPost, "/api/den/sync", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("lead syncing Den: want 403, got %d", rec.Code)
	}

	// The lead's own record: phone may change, nickname (login name) may not.
	var me map[string]any
	_ = json.Unmarshal(admin(http.MethodGet, "/api/people/"+leadID, nil).Body.Bytes(), &me)
	me["phone"] = "+44 20 7946 0999"
	if rec = lead(http.MethodPut, "/api/people/"+leadID, personInput(me)); rec.Code != http.StatusOK {
		t.Fatalf("lead changing own phone: %d %s", rec.Code, rec.Body.String())
	}
	me["nickname"] = uniqueNick("renamed")
	if rec = lead(http.MethodPut, "/api/people/"+leadID, personInput(me)); rec.Code != http.StatusForbidden {
		t.Fatalf("lead changing own nickname: want 403, got %d %s", rec.Code, rec.Body.String())
	}
	if rec = lead(http.MethodPost, "/api/people/"+leadID+"/status", map[string]string{"status": "paused"}); rec.Code != http.StatusForbidden {
		t.Fatalf("lead pausing themselves: want 403, got %d", rec.Code)
	}

	viewer := clientAs(router, "viewer", "hr-viewer")
	if rec = viewer(http.MethodGet, "/api/people/"+inB, nil); rec.Code != http.StatusOK {
		t.Fatalf("viewer reading: %d", rec.Code)
	}
	if rec = viewer(http.MethodPut, "/api/people/"+inB, map[string]any{}); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer writing: want 403, got %d", rec.Code)
	}
	nobody := clientAs(router, "volunteer", "stash-volunteer")
	if rec = nobody(http.MethodGet, "/api/people", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("volunteer without HR role: want 403, got %d", rec.Code)
	}

	var auditActor string
	_ = pool.QueryRow(context.Background(), `
SELECT actor_issuer || '|' || actor_subject FROM audit_events WHERE entity_id = $1 AND action = 'person.update'
 ORDER BY occurred_at DESC LIMIT 1`, leadID).Scan(&auditActor)
	if auditActor != "dev:|"+leadUser {
		t.Fatalf("audit actor = %q, want issuer and subject of the lead", auditActor)
	}
}

// personInput keeps the writeable fields of a person JSON object.
func personInput(p map[string]any) map[string]any {
	out := map[string]any{}
	for _, k := range []string{"first_name", "last_name", "nickname", "date_of_birth", "email", "org_email", "phone",
		"address", "social_handles", "emergency_contact", "notes"} {
		if v, ok := p[k]; ok {
			out[k] = v
		}
	}
	return out
}
