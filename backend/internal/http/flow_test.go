package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/AGlElectronics/hr/backend/internal/config"
	hrhttp "github.com/AGlElectronics/hr/backend/internal/http"
	"github.com/AGlElectronics/hr/backend/internal/provisioners"
)

// People validation against a real Postgres: required email, names without
// symbols, real phone numbers, unique email and login name.
// Skips unless HR_TEST_DATABASE_URL is set.
func TestPeopleValidation(t *testing.T) {
	do, pool := newTestClient(t)

	noEmail := do(http.MethodPost, "/api/people", map[string]any{"first_name": "Ada", "last_name": "Lovelace"})
	if noEmail.Code != http.StatusUnprocessableEntity {
		t.Fatalf("person without email: want 422, got %d %s", noEmail.Code, noEmail.Body.String())
	}

	nick := uniqueNick("ada")
	email := uniqueEmail("ada")
	bad := completePerson("Ada🦊", "Lovelace", email, nick)
	bad["phone"] = "+999 123"
	rec := do(http.MethodPost, "/api/people", bad)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), `"first_name"`) || !strings.Contains(rec.Body.String(), `"phone"`) {
		t.Fatalf("emoji name / fake phone: want 422 on first_name and phone, got %d %s", rec.Code, rec.Body.String())
	}

	personRec := do(http.MethodPost, "/api/people", completePerson("Ada", "O'Brien-Lovelace", email, nick))
	if personRec.Code != http.StatusCreated {
		t.Fatalf("create person %d %s", personRec.Code, personRec.Body.String())
	}
	var person struct {
		ID       string `json:"id"`
		Username string `json:"username"`
		Phone    string `json:"phone"`
	}
	_ = json.Unmarshal(personRec.Body.Bytes(), &person)
	cleanupPerson(t, pool, person.ID)
	if person.Username != nick || person.Phone != "+442079460000" {
		t.Fatalf("username/phone not normalized: %s", personRec.Body.String())
	}

	dup := do(http.MethodPost, "/api/people", completePerson("Other", "Ada", strings.ToUpper(email), uniqueNick("other")))
	if dup.Code != http.StatusUnprocessableEntity || !strings.Contains(dup.Body.String(), `"email"`) {
		t.Fatalf("duplicate email: want 422, got %d %s", dup.Code, dup.Body.String())
	}
	dupNick := do(http.MethodPost, "/api/people", completePerson("Other", "Ada", uniqueEmail("other"), nick))
	if dupNick.Code != http.StatusUnprocessableEntity || !strings.Contains(dupNick.Body.String(), `"nickname"`) {
		t.Fatalf("duplicate nickname: want 422, got %d %s", dupNick.Code, dupNick.Body.String())
	}
}

// Onboarding wizard: department → page 1 (profile + department) → page 2
// (access, prefilled from department defaults) → page 3 complete.
func TestOnboardingWizard(t *testing.T) {
	do, pool := newTestClient(t)

	slug := fmt.Sprintf("conops-%d", time.Now().UnixNano())
	deptRec := do(http.MethodPost, "/api/departments", map[string]any{
		"name": "Con Ops", "slug": slug,
		"default_roles": []map[string]string{{"role_name": "stash:volunteer", "group_name": "stash-volunteer"}},
	})
	if deptRec.Code != http.StatusCreated {
		t.Fatalf("create department %d %s", deptRec.Code, deptRec.Body.String())
	}
	var dept struct {
		ID        string `json:"id"`
		GroupName string `json:"group_name"`
	}
	_ = json.Unmarshal(deptRec.Body.Bytes(), &dept)
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM employment_drafts WHERE department_id = $1`, dept.ID)
		_, _ = pool.Exec(ctx, `DELETE FROM departments WHERE id = $1`, dept.ID)
	})
	if dept.GroupName != "dept-"+slug {
		t.Fatalf("group name %q", dept.GroupName)
	}

	incomplete := do(http.MethodPost, "/api/onboardings", map[string]any{
		"department_id": dept.ID,
		"person":        map[string]any{"first_name": "Grace", "last_name": "Hopper", "email": uniqueEmail("grace")},
	})
	if incomplete.Code < 400 || incomplete.Code >= 500 {
		t.Fatalf("incomplete profile: want 4xx, got %d %s", incomplete.Code, incomplete.Body.String())
	}

	startRec := do(http.MethodPost, "/api/onboardings", map[string]any{
		"department_id": dept.ID,
		"person":        completePerson("Grace", "Hopper", uniqueEmail("grace"), uniqueNick("grace")),
	})
	if startRec.Code != http.StatusCreated {
		t.Fatalf("start onboarding %d %s", startRec.Code, startRec.Body.String())
	}
	var view struct {
		Draft struct {
			ID    string `json:"id"`
			Title string `json:"title"`
			Roles []struct {
				RoleName string `json:"role_name"`
			} `json:"roles"`
		} `json:"draft"`
		Person struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"person"`
	}
	_ = json.Unmarshal(startRec.Body.Bytes(), &view)
	cleanupPerson(t, pool, view.Person.ID)
	if len(view.Draft.Roles) != 1 || view.Draft.Roles[0].RoleName != "stash:volunteer" {
		t.Fatalf("department defaults not prefilled: %s", startRec.Body.String())
	}
	if view.Draft.Title != "Con Ops volunteer" || view.Person.Status != "onboarding" {
		t.Fatalf("unexpected title/status: %s", startRec.Body.String())
	}

	rolesRec := do(http.MethodPut, "/api/drafts/"+view.Draft.ID+"/roles", map[string]any{
		"roles": []map[string]string{
			{"role_name": "stash:volunteer", "group_name": "stash-volunteer"},
			{"role_name": "stash:lead", "group_name": "stash-lead"},
		},
	})
	if rolesRec.Code >= 300 {
		t.Fatalf("assign roles %d %s", rolesRec.Code, rolesRec.Body.String())
	}

	completeRec := do(http.MethodPost, "/api/onboardings/"+view.Draft.ID+"/complete", nil)
	if completeRec.Code != http.StatusOK {
		t.Fatalf("complete %d %s", completeRec.Code, completeRec.Body.String())
	}
	var res struct {
		OK            bool     `json:"ok"`
		GroupsApplied []string `json:"groups_applied"`
		SetupSent     bool     `json:"password_setup_email_sent"`
		Draft         struct {
			State string `json:"state"`
		} `json:"draft"`
	}
	_ = json.Unmarshal(completeRec.Body.Bytes(), &res)
	if !res.OK || res.Draft.State != "active" || !res.SetupSent {
		t.Fatalf("complete result: %s", completeRec.Body.String())
	}
	want := map[string]bool{"dept-" + slug: true, "stash-volunteer": true, "stash-lead": true}
	for _, g := range res.GroupsApplied {
		delete(want, g)
	}
	if len(want) > 0 {
		t.Fatalf("missing groups %v in %v", want, res.GroupsApplied)
	}

	pRec := do(http.MethodGet, "/api/people/"+view.Person.ID, nil)
	_ = json.Unmarshal(pRec.Body.Bytes(), &view.Person)
	if view.Person.Status != "active" {
		t.Fatalf("person status want active, got %q", view.Person.Status)
	}

	again := do(http.MethodPost, "/api/onboardings/"+view.Draft.ID+"/complete", nil)
	if again.Code != http.StatusConflict {
		t.Fatalf("second complete: want 409, got %d", again.Code)
	}

	runLifecycle(t, do, pool, view.Person.ID, view.Draft.ID, dept.ID)
}

// runLifecycle covers what happens after onboarding: change access, pause,
// reactivate, offboard, and the dashboard/department counts.
func runLifecycle(t *testing.T, do testClient, pool *pgxpool.Pool, personID, draftID, deptID string) {
	t.Helper()
	type access struct {
		ExternalID  string `json:"external_id"`
		Memberships []struct {
			DraftID string `json:"draft_id"`
			Roles   []struct {
				RoleName string `json:"role_name"`
			} `json:"roles"`
		} `json:"memberships"`
	}
	var acc access
	rec := do(http.MethodGet, "/api/people/"+personID+"/access", nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &acc)
	if rec.Code != http.StatusOK || acc.ExternalID == "" || len(acc.Memberships) != 1 || len(acc.Memberships[0].Roles) != 2 {
		t.Fatalf("access %d %s", rec.Code, rec.Body.String())
	}

	rec = do(http.MethodPut, "/api/people/"+personID+"/access", map[string]any{
		"draft_id": draftID,
		"roles":    []map[string]string{{"role_name": "stash:volunteer", "group_name": "stash-volunteer"}},
	})
	acc = access{}
	_ = json.Unmarshal(rec.Body.Bytes(), &acc)
	if rec.Code != http.StatusOK || len(acc.Memberships[0].Roles) != 1 {
		t.Fatalf("change access %d %s", rec.Code, rec.Body.String())
	}

	statusOf := func(rec *httptest.ResponseRecorder) string {
		var p struct {
			Status string `json:"status"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &p)
		return p.Status
	}
	if rec = do(http.MethodPost, "/api/people/"+personID+"/status", map[string]string{"status": "paused"}); statusOf(rec) != "paused" {
		t.Fatalf("pause %d %s", rec.Code, rec.Body.String())
	}
	if rec = do(http.MethodPost, "/api/people/"+personID+"/status", map[string]string{"status": "active"}); statusOf(rec) != "active" {
		t.Fatalf("reactivate %d %s", rec.Code, rec.Body.String())
	}

	var dash struct {
		Departments []struct {
			ID            string `json:"id"`
			ActiveMembers int    `json:"active_members"`
		} `json:"departments"`
	}
	rec = do(http.MethodGet, "/api/dashboard", nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &dash)
	found := false
	for _, d := range dash.Departments {
		if d.ID == deptID {
			found = d.ActiveMembers == 1
		}
	}
	if rec.Code != http.StatusOK || !found {
		t.Fatalf("dashboard %d %s", rec.Code, rec.Body.String())
	}

	if rec = do(http.MethodPost, "/api/people/"+personID+"/offboard", map[string]any{}); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("offboard without reason: want 422, got %d", rec.Code)
	}
	// The stored account id no longer exists in the IdP: offboarding still
	// works and says so, instead of failing on the 404.
	if _, err := pool.Exec(context.Background(),
		`UPDATE people SET idp_user_id = 'deleted-in-keycloak' WHERE id = $1`, personID); err != nil {
		t.Fatal(err)
	}
	var accState struct {
		Account struct {
			State string `json:"state"`
		} `json:"account"`
	}
	rec = do(http.MethodGet, "/api/people/"+personID+"/access", nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &accState)
	if accState.Account.State != "missing" {
		t.Fatalf("account state want missing: %s", rec.Body.String())
	}
	rec = do(http.MethodPut, "/api/people/"+personID+"/access", map[string]any{"draft_id": draftID, "roles": []any{}})
	if rec.Code != http.StatusConflict {
		t.Fatalf("change access with missing account: want 409, got %d %s", rec.Code, rec.Body.String())
	}

	rec = do(http.MethodPost, "/api/people/"+personID+"/offboard", map[string]any{"reason": "left the team"})
	var off struct {
		Username string   `json:"username"`
		Warnings []string `json:"warnings"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &off)
	if rec.Code != http.StatusOK || off.Username == "" || len(off.Warnings) != 1 {
		t.Fatalf("offboard %d %s", rec.Code, rec.Body.String())
	}
	// Offboarding deletes the person; only a minimal audit line remains.
	if rec = do(http.MethodGet, "/api/people/"+personID, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("offboarded person: want 404, got %d", rec.Code)
	}
	var lines int
	var meta string
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*), COALESCE(max(metadata::text), '') FROM audit_events WHERE entity_id = $1`, personID).Scan(&lines, &meta); err != nil {
		t.Fatal(err)
	}
	if lines != 1 || !strings.Contains(meta, off.Username) || strings.Contains(meta, "@") {
		t.Fatalf("audit after offboard: %d lines, %s", lines, meta)
	}
	if rec = do(http.MethodPost, "/api/people/"+personID+"/offboard", map[string]any{"reason": "again"}); rec.Code != http.StatusNotFound {
		t.Fatalf("second offboard: want 404, got %d", rec.Code)
	}
}

func TestSettingsOrgEmailDomain(t *testing.T) {
	do, pool := newTestClient(t)
	var before string
	_ = pool.QueryRow(context.Background(), `SELECT value FROM settings WHERE key = 'org_email_domain'`).Scan(&before)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `UPDATE settings SET value = $1 WHERE key = 'org_email_domain'`, before)
	})

	if rec := do(http.MethodPut, "/api/settings", map[string]string{"org_email_domain": "not a domain"}); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad domain: want 422, got %d", rec.Code)
	}
	rec := do(http.MethodPut, "/api/settings", map[string]string{"org_email_domain": "@Example.ORG"})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"org_email_domain":"example.org"`) {
		t.Fatalf("set domain %d %s", rec.Code, rec.Body.String())
	}
}

func uniqueEmail(name string) string {
	return fmt.Sprintf("%s+%d@example.test", name, time.Now().UnixNano())
}

// uniqueNick is a letters-only nickname that differs per run.
func uniqueNick(prefix string) string {
	n := time.Now().UnixNano()
	b := []byte(prefix)
	for i := 0; i < 8; i++ {
		b = append(b, byte('a'+n%26))
		n /= 26
	}
	return string(b)
}

func completePerson(first, last, email, nickname string) map[string]any {
	return map[string]any{
		"first_name":        first,
		"last_name":         last,
		"nickname":          nickname,
		"date_of_birth":     "1990-12-10",
		"address":           map[string]string{"street": "1 Example Street", "postal_code": "EC1A 1BB", "city": "London", "country": "GB"},
		"email":             email,
		"phone":             "+44 20 7946 0000",
		"social_handles":    map[string]string{"telegram": "@" + strings.ToLower(first)},
		"emergency_contact": map[string]string{"name": "Charles Babbage", "phone": "+44 20 7946 0001"},
	}
}

type testClient func(method, path string, body any) *httptest.ResponseRecorder

func newTestClient(t *testing.T) (testClient, *pgxpool.Pool) {
	t.Helper()
	return newTestClientWith(t, nil)
}

// newTestClientWith lets a test adjust config and dependencies (after the
// pool is open) before the router is built. Requests are made as the HR
// admin "test-hr-admin".
func newTestClientWith(t *testing.T, adjust func(pool *pgxpool.Pool, cfg *config.Config, deps *hrhttp.Deps)) (testClient, *pgxpool.Pool) {
	t.Helper()
	router, pool := newTestRouter(t, adjust)
	return clientAs(router, "test-hr-admin", ""), pool
}

// clientAs makes requests as a dev user with the given groups (comma
// separated; "" means an HR admin).
func clientAs(router http.Handler, user, groups string) testClient {
	return func(method, path string, body any) *httptest.ResponseRecorder {
		var rdr *bytes.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rdr = bytes.NewReader(b)
		} else {
			rdr = bytes.NewReader(nil)
		}
		req := httptest.NewRequest(method, path, rdr)
		req.Header.Set("X-Dev-User", user)
		if groups != "" {
			req.Header.Set("X-Dev-Groups", groups)
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
}

func newTestRouter(t *testing.T, adjust func(pool *pgxpool.Pool, cfg *config.Config, deps *hrhttp.Deps)) (http.Handler, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("HR_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("HR_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)

	cfg := &config.Config{
		AppEnv:         "development",
		AppBaseURL:     "http://localhost:8110",
		HTTPAddr:       ":0",
		AuthMode:       config.AuthModeDev,
		DevDefaultUser: "test-hr-admin",
		AdminGroups:    []string{"hr-admin"},
		ViewerGroups:   []string{"hr-viewer"},
		LeadGroups:     []string{"hr-lead"},
	}
	deps := hrhttp.Deps{
		Config:    cfg,
		Pool:      pool,
		Logger:    zerolog.Nop(),
		IdP:    provisioners.NewFakeIdP(zerolog.Nop()),
	}
	if adjust != nil {
		adjust(pool, cfg, &deps)
	}
	return hrhttp.NewRouter(deps, "test"), pool
}

// cleanupPerson removes a test person and everything hanging off them, since
// HR_TEST_DATABASE_URL is often the dev database.
func cleanupPerson(t *testing.T, pool *pgxpool.Pool, personID string) {
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM employment_drafts WHERE person_id = $1`, personID)
		_, _ = pool.Exec(ctx, `DELETE FROM people WHERE id = $1`, personID)
	})
}
