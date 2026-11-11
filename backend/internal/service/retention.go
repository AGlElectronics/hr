package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AGlElectronics/hr/backend/internal/audit"
	"github.com/AGlElectronics/hr/backend/internal/db"
)

// retentionActor is the audit actor of retention purges.
const retentionActor = "system:retention"

// RetentionService applies the retention rules: applicants untouched for
// Retention.ApplicantMonths are deleted, people paused for
// Retention.PausedMonths are offboarded. HR admins are emailed
// Retention.WarningDays before; without a delivered warning nothing is
// purged.
type RetentionService struct {
	pool      *pgxpool.Pool
	settings  *SettingsService
	lifecycle *LifecycleService
	notify    *AdminNotifier
	audit     *audit.Writer
	now       func() time.Time
}

// NewRetentionService wires a RetentionService.
func NewRetentionService(pool *pgxpool.Pool, settings *SettingsService, lifecycle *LifecycleService, notify *AdminNotifier, aud *audit.Writer) *RetentionService {
	return &RetentionService{pool: pool, settings: settings, lifecycle: lifecycle, notify: notify, audit: aud, now: time.Now}
}

// RetentionCandidate is a person the rules will purge.
type RetentionCandidate struct {
	PersonID    uuid.UUID  `json:"person_id"`
	DisplayName string     `json:"display_name"`
	Status      string     `json:"status"`
	DueAt       time.Time  `json:"due_at"`
	WarnedAt    *time.Time `json:"warned_at,omitempty"`
}

// Candidates lists the people whose purge is due within the warning period.
func (s *RetentionService) Candidates(ctx context.Context) ([]RetentionCandidate, RetentionSettings, error) {
	st, err := s.settings.Get(ctx)
	if err != nil {
		return nil, RetentionSettings{}, err
	}
	r := st.Retention
	rows, err := s.pool.Query(ctx, `
SELECT id, display_name, status, due, retention_warned_at FROM (
  SELECT id, display_name, status, retention_warned_at,
         CASE WHEN status = 'applicant' THEN updated_at + make_interval(months => $1)
              ELSE COALESCE(paused_at, updated_at) + make_interval(months => $2) END AS due
    FROM people
   WHERE status = 'applicant'
      OR status = 'paused'
) c
 WHERE due - make_interval(days => $3) <= $4
 ORDER BY due`, r.ApplicantMonths, r.PausedMonths, r.WarningDays, s.now())
	if err != nil {
		return nil, r, err
	}
	defer rows.Close()
	out := []RetentionCandidate{}
	for rows.Next() {
		var c RetentionCandidate
		if err := rows.Scan(&c.PersonID, &c.DisplayName, &c.Status, &c.DueAt, &c.WarnedAt); err != nil {
			return nil, r, err
		}
		out = append(out, c)
	}
	return out, r, rows.Err()
}

// Run warns about upcoming purges and purges what is due and was warned
// about at least WarningDays ago.
func (s *RetentionService) Run(ctx context.Context) error {
	cands, r, err := s.Candidates(ctx)
	if err != nil || !r.Enabled || len(cands) == 0 {
		return err
	}
	now := s.now()
	var toWarn []RetentionCandidate
	for _, c := range cands {
		if c.WarnedAt == nil {
			toWarn = append(toWarn, c)
		}
	}
	if len(toWarn) > 0 {
		if err := s.notify.Send(ctx, "HR will delete personal data soon", retentionWarning(toWarn, r)); err != nil {
			return fmt.Errorf("retention: could not warn HR admins, nothing purged: %w", err)
		}
		ids := make([]uuid.UUID, 0, len(toWarn))
		for _, c := range toWarn {
			ids = append(ids, c.PersonID)
		}
		if _, err := s.pool.Exec(ctx, `UPDATE people SET retention_warned_at = $2 WHERE id = ANY($1)`, ids, now); err != nil {
			return err
		}
	}
	grace := time.Duration(r.WarningDays) * 24 * time.Hour
	var failed []string
	for _, c := range cands {
		if c.WarnedAt == nil || c.DueAt.After(now) || now.Sub(*c.WarnedAt) < grace {
			continue
		}
		if err := s.purge(ctx, c, r); err != nil {
			failed = append(failed, c.DisplayName+": "+err.Error())
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("retention: %s", strings.Join(failed, "; "))
	}
	return nil
}

func (s *RetentionService) purge(ctx context.Context, c RetentionCandidate, r RetentionSettings) error {
	if c.Status == "paused" {
		_, err := s.lifecycle.Offboard(ctx, retentionActor, c.PersonID, OffboardInput{
			Reason: fmt.Sprintf("Paused for more than %d months (retention period)", r.PausedMonths),
		})
		return err
	}
	var username string
	if err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT COALESCE(username, display_name) FROM people WHERE id = $1 AND status = 'applicant'`, c.PersonID).Scan(&username); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
DELETE FROM audit_events WHERE entity_id = $1 OR entity_id IN (SELECT id FROM employment_drafts WHERE person_id = $1)`, c.PersonID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM employment_drafts WHERE person_id = $1`, c.PersonID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM people WHERE id = $1`, c.PersonID)
		return err
	}); err != nil {
		return err
	}
	if err := s.audit.Record(ctx, retentionActor, "person.retention_purge", "person", c.PersonID, map[string]any{
		"username": username, "reason": fmt.Sprintf("applicant untouched for %d months", r.ApplicantMonths),
	}); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, `UPDATE audit_events SET purge_after = now() + make_interval(months => $2) WHERE entity_id = $1`,
		c.PersonID, r.OffboardedAuditMonths)
	return err
}

func retentionWarning(cs []RetentionCandidate, r RetentionSettings) string {
	var b strings.Builder
	b.WriteString("Hi,\n\nHR's retention rules will remove these people soon:\n\n")
	for _, c := range cs {
		what := "deleted (applicant)"
		if c.Status == "paused" {
			what = "offboarded and their account deleted (paused)"
		}
		fmt.Fprintf(&b, "  %s: %s on or after %s\n", c.DisplayName, what, c.DueAt.Format("2 January 2006"))
	}
	fmt.Fprintf(&b, "\nThe rules: applicants untouched for %d months, people paused for %d months.\n", r.ApplicantMonths, r.PausedMonths)
	fmt.Fprintf(&b, "To keep someone, open them in HR and update or reactivate them before then. Nothing is removed until %d days after this email.\n", r.WarningDays)
	return b.String()
}
