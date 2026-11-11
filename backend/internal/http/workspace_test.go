package http_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/AGlElectronics/hr/backend/internal/config"
	hrhttp "github.com/AGlElectronics/hr/backend/internal/http"
	"github.com/AGlElectronics/hr/backend/internal/mail"
	"github.com/AGlElectronics/hr/backend/internal/provisioners"
	"github.com/AGlElectronics/hr/backend/internal/service"
)

type memMailer struct {
	mu   sync.Mutex
	sent []mail.Message
}

func (m *memMailer) Send(_ context.Context, msg mail.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, msg)
	return nil
}

func (m *memMailer) find(to, subject string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.sent {
		if s.To == to && strings.Contains(s.Subject, subject) {
			return true
		}
	}
	return false
}

// TestWorkspaceConnector: onboarding creates the Workspace account and adds
// it to the department group, offboarding suspends it, and the clean-up
// reminds HR admins before an account is deleted.
func TestWorkspaceConnector(t *testing.T) {
	gw := provisioners.NewFakeWorkspace()
	mailer := &memMailer{}
	services := &hrhttp.Services{}
	router, pool := newTestRouter(t, func(pool *pgxpool.Pool, _ *config.Config, deps *hrhttp.Deps) {
		deps.IdP = provisioners.NewFakeIdP(zerolog.Nop())
		deps.Jobs = service.NewJobQueue(pool, zerolog.Nop(), true)
		deps.Mailer = mailer
		deps.Services = services
		deps.Workspace = func(_ context.Context, t provisioners.WorkspaceTarget) (provisioners.WorkspaceDirectory, error) {
			if t.AdminEmail != "gadmin@con.example" || t.Domain != "con.example" {
				return nil, nil
			}
			return gw, nil
		}
	})
	admin := clientAs(router, "test-hr-admin", "")
	ctx := context.Background()
	var before []byte
	_ = pool.QueryRow(ctx, `SELECT json_object_agg(key, value) FROM settings WHERE key LIKE 'workspace_%' OR key = 'admin_notification_emails'`).Scan(&before)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM settings WHERE key LIKE 'workspace_%' OR key = 'admin_notification_emails'`)
		var m map[string]string
		if json.Unmarshal(before, &m) == nil {
			for k, v := range m {
				_, _ = pool.Exec(ctx, `INSERT INTO settings (key, value) VALUES ($1, $2)`, k, v)
			}
		}
	})
	rec := admin(http.MethodPut, "/api/settings", map[string]any{
		"admin_notification_emails": "hr@con.example",
		"workspace":                 map[string]any{"enabled": true, "domain": "con.example", "admin_email": "gadmin@con.example", "org_unit": "/Volunteers", "delete_after_days": 30, "reminder_days": 7},
	})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"available":true`) {
		t.Fatalf("workspace settings %d %s", rec.Code, rec.Body.String())
	}

	dept, group := createDepartment(t, admin, pool, "Gws")
	first := strings.ToUpper(uniqueNick("w")[:1]) + uniqueNick("anda")[1:]
	person, draft := startOnboarding(t, admin, pool, dept, first)
	completeOnboarding(t, admin, draft)
	email := strings.ToLower(first) + ".tester@con.example"
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM workspace_accounts WHERE primary_email = $1`, email) })

	u := gw.User(email)
	if u == nil || u.OrgUnitPath != "/Volunteers" || !strings.Contains(u.RecoveryEmail, "@example.test") {
		t.Fatalf("workspace account %s: %+v (users %v)", email, u, gw.Users)
	}
	members, _ := gw.ListMembers(ctx, group+"@con.example")
	if len(members) != 1 || members[0] != email {
		t.Fatalf("department group members: %v", members)
	}
	var p struct {
		OrgEmail string `json:"org_email"`
		Email    string `json:"email"`
	}
	_ = json.Unmarshal(admin(http.MethodGet, "/api/people/"+person, nil).Body.Bytes(), &p)
	if p.OrgEmail != email || !mailer.find(p.Email, "Google Workspace account") {
		t.Fatalf("org email %q / welcome email sent: %v", p.OrgEmail, mailer.sent)
	}

	if rec = admin(http.MethodPost, "/api/people/"+person+"/offboard", map[string]any{"reason": "left"}); rec.Code != http.StatusOK {
		t.Fatalf("offboard %d %s", rec.Code, rec.Body.String())
	}
	if u = gw.User(email); u == nil || !u.Suspended {
		t.Fatalf("offboarding should suspend the account: %+v", u)
	}
	var accts struct {
		Items []struct {
			ID          string  `json:"id"`
			Status      string  `json:"status"`
			DeleteAfter *string `json:"delete_after"`
		} `json:"items"`
	}
	_ = json.Unmarshal(admin(http.MethodGet, "/api/workspace/accounts?status=suspended", nil).Body.Bytes(), &accts)
	var acct string
	for _, a := range accts.Items {
		var e string
		_ = pool.QueryRow(ctx, `SELECT primary_email FROM workspace_accounts WHERE id = $1`, a.ID).Scan(&e)
		if e == email && a.DeleteAfter != nil {
			acct = a.ID
		}
	}
	if acct == "" {
		t.Fatalf("suspended account not listed: %+v", accts)
	}

	if _, err := pool.Exec(ctx, `UPDATE workspace_accounts SET delete_after = now() - interval '1 day' WHERE id = $1`, acct); err != nil {
		t.Fatal(err)
	}
	if err := services.Workspace.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}
	if gw.User(email) == nil || !mailer.find("hr@con.example", "will be deleted soon") {
		t.Fatal("the first clean-up must remind HR admins and not delete yet")
	}
	if rec = admin(http.MethodPost, "/api/workspace/accounts/"+acct+"/delete", nil); rec.Code != http.StatusOK || gw.User(email) != nil {
		t.Fatalf("delete now %d %s", rec.Code, rec.Body.String())
	}
	var deletedAudit bool
	_ = pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM audit_events WHERE entity_id = $1 AND action = 'workspace.delete')`, person).Scan(&deletedAudit)
	if !deletedAudit {
		t.Fatal("workspace deletion not audited")
	}
}
