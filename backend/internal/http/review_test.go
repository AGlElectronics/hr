package http_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/AGlElectronics/hr/backend/internal/provisioners"
)

type reviewItem struct {
	ID            string `json:"id"`
	Source        string `json:"source"`
	Status        string `json:"status"`
	IDPUserID     string `json:"idp_user_id"`
	Email         string `json:"email"`
	MatchKind     string `json:"match_kind"`
	MatchPersonID string `json:"match_person_id"`
	PersonID      string `json:"person_id"`
}

func reviewItems(t *testing.T, do testClient, query string) []reviewItem {
	t.Helper()
	var page struct {
		Items []reviewItem `json:"items"`
	}
	rec := do(http.MethodGet, "/api/review?limit=200&"+query, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("review list %d %s", rec.Code, rec.Body.String())
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &page)
	return page.Items
}

func findItem(items []reviewItem, f func(reviewItem) bool) *reviewItem {
	for i := range items {
		if f(items[i]) {
			return &items[i]
		}
	}
	return nil
}

// TestReviewQueue: the scan queues accounts without an hrPersonId, matches
// only verified emails, never accepts by itself; reject disables the
// account, accept creates the person linked to it, merge links an account
// to an existing person.
func TestReviewQueue(t *testing.T) {
	router, pool, idp, _ := fakeIDPRouter(t)
	admin := clientAs(router, "test-hr-admin", "")
	ctx := context.Background()
	dept, _ := createDepartment(t, admin, pool, "Review")
	existing, _ := startOnboarding(t, admin, pool, dept, "Erik")
	var existingEmail string
	_ = pool.QueryRow(ctx, `SELECT email FROM people WHERE id = $1`, existing).Scan(&existingEmail)

	suffix := uniqueNick("")
	users := []provisioners.UserInfo{
		{ID: "kc-verified-" + suffix, Username: "erik.sso", Email: existingEmail, EmailVerified: true, Enabled: true, FirstName: "Erik"},
		{ID: "kc-unverified-" + suffix, Username: "mallory", Email: strings.ToUpper(existingEmail), Enabled: true},
		{ID: "kc-new-" + suffix, Username: "nina", Email: "nina+" + suffix + "@example.test", EmailVerified: true, Enabled: true, FirstName: "Nina", LastName: "Neu"},
		{ID: "kc-hr-" + suffix, Username: "known", Enabled: true, Attributes: map[string]string{"hrPersonId": existing}},
		{ID: "kc-off-" + suffix, Username: "disabled", Enabled: false},
	}
	idp.SetUsers(users)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM review_items WHERE idp_user_id LIKE '%' || $1`, suffix) })

	rec := admin(http.MethodPost, "/api/review/scan", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"created":3`) {
		t.Fatalf("scan %d %s", rec.Code, rec.Body.String())
	}
	items := reviewItems(t, admin, "source=idp_scan")
	verified := findItem(items, func(i reviewItem) bool { return i.IDPUserID == users[0].ID })
	unverified := findItem(items, func(i reviewItem) bool { return i.IDPUserID == users[1].ID })
	fresh := findItem(items, func(i reviewItem) bool { return i.IDPUserID == users[2].ID })
	if verified == nil || unverified == nil || fresh == nil {
		t.Fatalf("scan items: %+v", items)
	}
	if verified.MatchKind != "verified_email" || verified.MatchPersonID != existing || unverified.MatchKind != "possible" || fresh.MatchKind != "" {
		t.Fatalf("matching: verified=%+v unverified=%+v fresh=%+v", verified, unverified, fresh)
	}
	if verified.Status != "pending" {
		t.Fatal("a verified match must still wait for a human")
	}
	if rec = admin(http.MethodPost, "/api/review/scan", nil); !strings.Contains(rec.Body.String(), `"created":0`) {
		t.Fatalf("second scan must not duplicate: %s", rec.Body.String())
	}

	rec = admin(http.MethodPost, "/api/review/"+unverified.ID+"/reject", map[string]string{"reason": "not one of ours"})
	if rec.Code != http.StatusOK || idp.Enabled(users[1].ID) {
		t.Fatalf("reject should disable the account: %d %s", rec.Code, rec.Body.String())
	}

	rec = admin(http.MethodPost, "/api/review/"+verified.ID+"/merge", map[string]any{"person_id": existing})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"merged"`) {
		t.Fatalf("merge %d %s", rec.Code, rec.Body.String())
	}
	var linked string
	_ = pool.QueryRow(ctx, `SELECT COALESCE(idp_user_id, '') FROM people WHERE id = $1`, existing).Scan(&linked)
	if linked != users[0].ID {
		t.Fatalf("merge should link the account, person has %q", linked)
	}

	person := completePerson("Nina", "Neu", "", uniqueNick("nina"))
	delete(person, "email")
	rec = admin(http.MethodPost, "/api/review/"+fresh.ID+"/accept", map[string]any{"person": person, "department_id": dept, "complete": true})
	if rec.Code != http.StatusOK {
		t.Fatalf("accept %d %s", rec.Code, rec.Body.String())
	}
	var acc struct {
		Onboarding struct {
			Draft  struct{ ID string } `json:"draft"`
			Person struct {
				ID        string `json:"id"`
				Email     string `json:"email"`
				IDPUserID string `json:"idp_user_id"`
			} `json:"person"`
		} `json:"onboarding"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &acc)
	cleanupPerson(t, pool, acc.Onboarding.Person.ID)
	if acc.Onboarding.Person.IDPUserID != users[2].ID || acc.Onboarding.Person.Email != users[2].Email {
		t.Fatalf("accepted person: %s", rec.Body.String())
	}
	if st := waitProvisioning(t, admin, acc.Onboarding.Draft.ID); st != "succeeded" {
		t.Fatalf("provisioning after accept: %s", st)
	}
	if calls := idp.Calls(); calls[len(calls)-1].ExternalID != users[2].ID {
		t.Fatalf("provisioning should update the linked account, got %+v", calls[len(calls)-1])
	}
}

// TestSelfServiceRegistration: a signed-in volunteer without HR roles fills
// in their own entry; the lead of the chosen department reviews it.
func TestSelfServiceRegistration(t *testing.T) {
	router, pool, _, _ := fakeIDPRouter(t)
	admin := clientAs(router, "test-hr-admin", "")
	dept, _ := createDepartment(t, admin, pool, "Selfserve")
	sub := uniqueNick("volsub")
	email := sub + "@example.test"
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM review_items WHERE idp_subject = $1`, sub) })
	vol := func(method, path string, body any) (int, []byte) {
		rec := clientAsWithEmail(router, sub, "stash-volunteer", email)(method, path, body)
		res := rec.Result()
		defer func() { _ = res.Body.Close() }()
		raw, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		return res.StatusCode, raw
	}

	if status, _ := vol(http.MethodGet, "/api/self-service/registration", nil); status != http.StatusNotFound {
		t.Fatalf("no entry yet: want 404, got %d", status)
	}
	status, raw := vol(http.MethodPut, "/api/self-service/registration", map[string]any{"first_name": "Vera", "last_name": "Volunteer", "department_id": dept})
	var v struct {
		Status        string            `json:"status"`
		Email         string            `json:"email"`
		MissingFields map[string]string `json:"missing_fields"`
	}
	_ = json.Unmarshal(raw, &v)
	if status != http.StatusOK || v.Status != "pending" || v.Email != email || v.MissingFields["phone"] == "" {
		t.Fatalf("partial registration: %d %+v", status, v)
	}
	full := completePerson("Vera", "Volunteer", email, uniqueNick("vera"))
	delete(full, "email")
	full["department_id"] = dept
	_, raw = vol(http.MethodPut, "/api/self-service/registration", full)
	_ = json.Unmarshal(raw, &v)
	if v.Status != "ready" {
		t.Fatalf("complete registration should be ready: %+v", v)
	}
	// The volunteer has no HR role: the queue itself stays closed.
	if status, _ := vol(http.MethodGet, "/api/review", nil); status != http.StatusForbidden {
		t.Fatalf("volunteer reading the queue: want 403, got %d", status)
	}

	leadID, _ := startOnboarding(t, admin, pool, dept, "Lotte")
	if _, err := pool.Exec(context.Background(), `UPDATE people SET idp_subject = $2 WHERE id = $1`, leadID, "lead-"+sub); err != nil {
		t.Fatal(err)
	}
	admin(http.MethodPut, "/api/departments/"+dept+"/leads", map[string]any{"person_ids": []string{leadID}})
	lead := clientAs(router, "lead-"+sub, "hr-lead")
	item := findItem(reviewItems(t, lead, "source=self_service"), func(i reviewItem) bool { return i.Email == email })
	if item == nil || item.Status != "ready" {
		t.Fatal("the lead of the chosen department should see the ready entry")
	}
	full["email"] = email
	delete(full, "department_id")
	other, _ := createDepartment(t, admin, pool, "Otherdept")
	if rec := lead(http.MethodPost, "/api/review/"+item.ID+"/accept", map[string]any{"person": full, "department_id": other}); rec.Code != http.StatusForbidden {
		t.Fatalf("lead accepting into another department: want 403, got %d %s", rec.Code, rec.Body.String())
	}
	rec := lead(http.MethodPost, "/api/review/"+item.ID+"/accept", map[string]any{"person": full, "department_id": dept})
	if rec.Code != http.StatusOK {
		t.Fatalf("lead accept %d %s", rec.Code, rec.Body.String())
	}
	var subject string
	_ = pool.QueryRow(context.Background(), `SELECT COALESCE(idp_subject, '') FROM people WHERE lower(email) = $1`, email).Scan(&subject)
	if subject != sub {
		t.Fatalf("accepted person should carry the volunteer's subject, got %q", subject)
	}
	_, _ = pool.Exec(context.Background(), `DELETE FROM employment_drafts WHERE person_id IN (SELECT id FROM people WHERE lower(email) = $1)`, email)
	_, _ = pool.Exec(context.Background(), `DELETE FROM people WHERE lower(email) = $1`, email)
	delete(full, "email")
	if status, _ := vol(http.MethodPut, "/api/self-service/registration", full); status != http.StatusConflict {
		t.Fatalf("editing after the decision: want 409, got %d", status)
	}
}
