package service

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/AGlElectronics/hr/backend/internal/audit"
	"github.com/AGlElectronics/hr/backend/internal/mail"
)

type fakeMailer struct {
	mu   sync.Mutex
	sent []mail.Message
	fail error
}

func (m *fakeMailer) Send(_ context.Context, msg mail.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail != nil {
		return m.fail
	}
	m.sent = append(m.sent, msg)
	return nil
}

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HR_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("HR_TEST_DATABASE_URL not set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// withSetting sets an HR setting for the test and restores it afterwards.
func withSetting(t *testing.T, pool *pgxpool.Pool, key, value string) {
	t.Helper()
	ctx := context.Background()
	var before *string
	_ = pool.QueryRow(ctx, `SELECT value FROM settings WHERE key = $1`, key).Scan(&before)
	if _, err := pool.Exec(ctx, `INSERT INTO settings (key, value) VALUES ($1, $2) ON CONFLICT (key) DO UPDATE SET value = $2`, key, value); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if before == nil {
			_, _ = pool.Exec(ctx, `DELETE FROM settings WHERE key = $1`, key)
		} else {
			_, _ = pool.Exec(ctx, `UPDATE settings SET value = $2 WHERE key = $1`, key, *before)
		}
	})
}

func insertPerson(t *testing.T, pool *pgxpool.Pool, name, status string, updated, paused *time.Time) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := pool.Exec(context.Background(), `
INSERT INTO people (id, legal_name, display_name, email, status, updated_at, paused_at)
VALUES ($1, $2, $2, $3, $4, $5, $6)`, id, name, fmt.Sprintf("%s+%d@example.test", strings.ToLower(name), time.Now().UnixNano()), status, updated, paused)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM people WHERE id = $1`, id) })
	return id
}

func exists(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) bool {
	var ok bool
	_ = pool.QueryRow(context.Background(), `SELECT EXISTS (SELECT 1 FROM people WHERE id = $1)`, id).Scan(&ok)
	return ok
}

func TestRetentionWarnsThenPurges(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	// Only this test's people may be candidates.
	if _, err := pool.Exec(ctx, `UPDATE people SET updated_at = now(), paused_at = CASE WHEN paused_at IS NULL THEN NULL ELSE now() END`); err != nil {
		t.Fatal(err)
	}
	withSetting(t, pool, "admin_notification_emails", "hr-lead@example.test")
	withSetting(t, pool, "retention_enabled", "true")

	aw := audit.NewWriter(pool, zerolog.Nop())
	settings := NewSettingsService(pool, aw, nil)
	people := NewPeopleService(pool, aw, settings, nil)
	drafts := NewDraftService(pool, aw)
	depts := NewDepartmentService(pool, aw, nil, nil, "")
	lifecycle := NewLifecycleService(pool, people, drafts, depts, nil, nil, settings, aw)
	mailer := &fakeMailer{}
	svc := NewRetentionService(pool, settings, lifecycle, NewAdminNotifier(settings, mailer, nil, nil), aw)

	old := time.Now().AddDate(0, -7, 0)
	longPaused := time.Now().AddDate(-2, -1, 0)
	fresh := time.Now()
	applicant := insertPerson(t, pool, "Oldapplicant", "applicant", &old, nil)
	paused := insertPerson(t, pool, "Longpaused", "paused", &longPaused, &longPaused)
	keep := insertPerson(t, pool, "Freshapplicant", "applicant", &fresh, nil)

	mailer.fail = fmt.Errorf("smtp down")
	if err := svc.Run(ctx); err == nil || !exists(t, pool, applicant) {
		t.Fatalf("without a delivered warning nothing may be purged: %v", err)
	}
	mailer.fail = nil

	if err := svc.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(mailer.sent) != 1 || !strings.Contains(mailer.sent[0].Body, "Oldapplicant") || !strings.Contains(mailer.sent[0].Body, "Longpaused") ||
		strings.Contains(mailer.sent[0].Body, "Freshapplicant") || mailer.sent[0].To != "hr-lead@example.test" {
		t.Fatalf("warning email: %+v", mailer.sent)
	}
	if !exists(t, pool, applicant) || !exists(t, pool, paused) {
		t.Fatal("purged on the same day as the warning")
	}

	svc.now = func() time.Time { return time.Now().Add(15 * 24 * time.Hour) }
	if err := svc.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if exists(t, pool, applicant) || exists(t, pool, paused) || !exists(t, pool, keep) {
		t.Fatalf("after the warning period: applicant=%v paused=%v fresh=%v", exists(t, pool, applicant), exists(t, pool, paused), exists(t, pool, keep))
	}
	if len(mailer.sent) != 1 {
		t.Fatalf("warned twice: %d emails", len(mailer.sent))
	}
	var purgeAfter *time.Time
	_ = pool.QueryRow(ctx, `SELECT purge_after FROM audit_events WHERE entity_id = $1 AND action = 'person.retention_purge'`, applicant).Scan(&purgeAfter)
	if purgeAfter == nil {
		t.Fatal("retention purge not audited with a purge date")
	}
	var offboarded bool
	_ = pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM audit_events WHERE entity_id = $1 AND action = 'person.offboard' AND actor_issuer = 'system')`, paused).Scan(&offboarded)
	if !offboarded {
		t.Fatal("paused person not offboarded by the system actor")
	}
}
