package http_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/AGlElectronics/hr/backend/internal/config"
	hrhttp "github.com/AGlElectronics/hr/backend/internal/http"
	"github.com/AGlElectronics/hr/backend/internal/provisioners"
	"github.com/AGlElectronics/hr/backend/internal/service"
)

func fakeIDPRouter(t *testing.T) (http.Handler, *pgxpool.Pool, *provisioners.FakeIDP, *service.JobQueue) {
	t.Helper()
	idp := provisioners.NewFakeIDP(zerolog.Nop())
	var jobs *service.JobQueue
	router, pool := newTestRouter(t, func(pool *pgxpool.Pool, _ *config.Config, deps *hrhttp.Deps) {
		deps.IDP = idp
		jobs = service.NewJobQueue(pool, zerolog.Nop(), true)
		deps.Jobs = jobs
	})
	return router, pool, idp, jobs
}

// TestPauseAndBanDisableTheAccount: pausing and banning disable the login,
// reactivating enables it again unless the person is banned.
func TestPauseAndBanDisableTheAccount(t *testing.T) {
	router, pool, idp, _ := fakeIDPRouter(t)
	admin := clientAs(router, "test-hr-admin", "")
	dept, _ := createDepartment(t, admin, pool, "Pause")
	person, draft := startOnboarding(t, admin, pool, dept, "Paula")
	completeOnboarding(t, admin, draft)
	ext := "idp:" + person

	if rec := admin(http.MethodPost, "/api/people/"+person+"/status", map[string]string{"status": "paused"}); rec.Code != http.StatusOK || idp.Enabled(ext) {
		t.Fatalf("pause should disable the account: %d %s enabled=%v", rec.Code, rec.Body.String(), idp.Enabled(ext))
	}
	var pausedAt *string
	_ = pool.QueryRow(context.Background(), `SELECT paused_at::text FROM people WHERE id = $1`, person).Scan(&pausedAt)
	if pausedAt == nil {
		t.Fatal("paused_at not set")
	}
	if rec := admin(http.MethodPost, "/api/people/"+person+"/status", map[string]string{"status": "active"}); rec.Code != http.StatusOK || !idp.Enabled(ext) {
		t.Fatalf("reactivate should enable the account: %d", rec.Code)
	}
	if rec := admin(http.MethodPost, "/api/people/"+person+"/ban", map[string]any{"banned": true}); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("ban without reason: want 422, got %d", rec.Code)
	}
	if rec := admin(http.MethodPost, "/api/people/"+person+"/ban", map[string]any{"banned": true, "reason": "harassment"}); rec.Code != http.StatusOK || idp.Enabled(ext) {
		t.Fatalf("ban should disable the account: %d %s", rec.Code, rec.Body.String())
	}
	// A banned person stays disabled through pause and reactivate.
	admin(http.MethodPost, "/api/people/"+person+"/status", map[string]string{"status": "paused"})
	admin(http.MethodPost, "/api/people/"+person+"/status", map[string]string{"status": "active"})
	if idp.Enabled(ext) {
		t.Fatal("reactivating a banned person enabled the account")
	}
	if rec := admin(http.MethodPost, "/api/people/"+person+"/ban", map[string]any{"banned": false}); rec.Code != http.StatusOK || !idp.Enabled(ext) {
		t.Fatalf("unban should enable an active person's account: %d", rec.Code)
	}
	if rec := admin(http.MethodPost, "/api/people/"+person+"/password-setup-email", nil); rec.Code != http.StatusOK || len(idp.SetupMails()) != 1 {
		t.Fatalf("resend set-password email: %d %s", rec.Code, rec.Body.String())
	}
	var actions string
	_ = pool.QueryRow(context.Background(), `SELECT string_agg(action, ',' ORDER BY occurred_at) FROM audit_events WHERE entity_id = $1 AND action LIKE 'person.%ban%'`, person).Scan(&actions)
	if actions != "person.ban,person.unban" {
		t.Fatalf("ban audit: %q", actions)
	}
}

// TestOffboardHookPayload: modules hear the issuer, subject and external id
// of who left.
func TestOffboardHookPayload(t *testing.T) {
	got := make(chan service.OffboardHookPayload, 1)
	var auth string
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		var p service.OffboardHookPayload
		_ = json.NewDecoder(r.Body).Decode(&p)
		got <- p
		w.WriteHeader(http.StatusNoContent)
	}))
	defer hook.Close()
	idp := provisioners.NewFakeIDP(zerolog.Nop())
	router, pool := newTestRouter(t, func(pool *pgxpool.Pool, cfg *config.Config, deps *hrhttp.Deps) {
		cfg.OffboardHooks = hook.URL + "|hook-test-token"
		deps.IDP = idp
		deps.Jobs = service.NewJobQueue(pool, zerolog.Nop(), true)
	})
	admin := clientAs(router, "test-hr-admin", "")
	dept, _ := createDepartment(t, admin, pool, "Hook")
	person, draft := startOnboarding(t, admin, pool, dept, "Hanna")
	completeOnboarding(t, admin, draft)
	if _, err := pool.Exec(context.Background(), `UPDATE people SET idp_subject = 'sub-hanna' WHERE id = $1`, person); err != nil {
		t.Fatal(err)
	}
	if rec := admin(http.MethodPost, "/api/people/"+person+"/offboard", map[string]any{"reason": "moved away"}); rec.Code != http.StatusOK {
		t.Fatalf("offboard %d %s", rec.Code, rec.Body.String())
	}
	p := <-got
	if p.Issuer != "dev:" || p.Subject != "sub-hanna" || p.ExternalID != "idp:"+person || p.IDPUserID != p.ExternalID || p.Username == "" {
		t.Fatalf("hook payload: %+v", p)
	}
	if auth != "Bearer hook-test-token" {
		t.Fatalf("hook auth %q", auth)
	}
}

// TestCancelOnboarding: an onboarding that is still a draft can be
// cancelled; the person goes back to applicant.
func TestCancelOnboarding(t *testing.T) {
	router, pool, _, _ := fakeIDPRouter(t)
	admin := clientAs(router, "test-hr-admin", "")
	dept, _ := createDepartment(t, admin, pool, "Cancel")
	person, draft := startOnboarding(t, admin, pool, dept, "Carl")

	rec := admin(http.MethodPost, "/api/onboardings/"+draft+"/cancel", map[string]string{"reason": "changed their mind"})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"state":"rejected"`) || !strings.Contains(rec.Body.String(), `"status":"applicant"`) {
		t.Fatalf("cancel %d %s", rec.Code, rec.Body.String())
	}
	if rec = admin(http.MethodPost, "/api/onboardings/"+draft+"/complete", nil); rec.Code != http.StatusConflict {
		t.Fatalf("complete after cancel: want 409, got %d", rec.Code)
	}
	if rec = admin(http.MethodPost, "/api/onboardings/"+draft+"/cancel", nil); rec.Code != http.StatusConflict {
		t.Fatalf("second cancel: want 409, got %d", rec.Code)
	}
	_ = person
}

// TestProvisioningFailureAndRecovery: a permanent IDP error puts the
// onboarding back to draft at once; a job whose worker died on its last
// attempt is given up and the onboarding recovered.
func TestProvisioningFailureAndRecovery(t *testing.T) {
	router, pool, idp, jobs := fakeIDPRouter(t)
	admin := clientAs(router, "test-hr-admin", "")
	dept, _ := createDepartment(t, admin, pool, "Fail")
	_, draft := startOnboarding(t, admin, pool, dept, "Fiona")

	idp.SetFailProvision(&provisioners.UsernameTakenError{Username: "fiona"})
	if rec := admin(http.MethodPost, "/api/onboardings/"+draft+"/complete", nil); rec.Code != http.StatusAccepted {
		t.Fatalf("complete: want 202, got %d %s", rec.Code, rec.Body.String())
	}
	if st := waitProvisioning(t, admin, draft); st != "failed" {
		t.Fatalf("permanent error: want failed, got %s", st)
	}
	rec := admin(http.MethodGet, "/api/onboardings/"+draft, nil)
	if !strings.Contains(rec.Body.String(), `"state":"draft"`) || !strings.Contains(rec.Body.String(), "already used by another account") {
		t.Fatalf("after a failed completion the onboarding is a draft again with the error: %s", rec.Body.String())
	}

	// Simulate a worker that died during the last attempt.
	idp.SetFailProvision(nil)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `UPDATE employment_drafts SET state = 'provisioning' WHERE id = $1`, draft); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]string{"draft_id": draft})
	if _, err := pool.Exec(ctx, `
INSERT INTO background_jobs (kind, payload, status, attempts, max_attempts, locked_until, created_by)
VALUES ('onboarding.complete', $1, 'running', 3, 3, now() - interval '1 minute', 'test-hr-admin')`, payload); err != nil {
		t.Fatal(err)
	}
	if err := jobs.RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	var state string
	_ = pool.QueryRow(ctx, `SELECT state FROM employment_drafts WHERE id = $1`, draft).Scan(&state)
	if state != "draft" {
		t.Fatalf("interrupted last attempt: draft should be back to draft, is %s", state)
	}
	completeOnboarding(t, admin, draft)
}
