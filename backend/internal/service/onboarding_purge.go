package service

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AGlElectronics/hr/backend/internal/audit"
	"github.com/AGlElectronics/hr/backend/internal/httperror"
)

// OnboardingPurge deletes stale employment drafts still in state "draft".
type OnboardingPurge struct {
	pool     *pgxpool.Pool
	settings *SettingsService
	audit    *audit.Writer
}

// NewOnboardingPurge wires the purge job.
func NewOnboardingPurge(pool *pgxpool.Pool, settings *SettingsService, aud *audit.Writer) *OnboardingPurge {
	return &OnboardingPurge{pool: pool, settings: settings, audit: aud}
}

// Run deletes draft onboardings older than onboarding_auto_delete_days.
// 0 days means the purge is off.
func (p *OnboardingPurge) Run(ctx context.Context) error {
	st, err := p.settings.Get(ctx)
	if err != nil {
		return err
	}
	days := st.OnboardingAutoDeleteDays
	if days <= 0 {
		return nil
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -days)
	rows, err := p.pool.Query(ctx, `
SELECT id, title FROM employment_drafts
 WHERE state = 'draft' AND created_at < $1`, cutoff)
	if err != nil {
		return httperror.Internal(err)
	}
	defer rows.Close()
	type hit struct {
		id    uuid.UUID
		title string
	}
	var list []hit
	for rows.Next() {
		var h hit
		if err := rows.Scan(&h.id, &h.title); err != nil {
			return httperror.Internal(err)
		}
		list = append(list, h)
	}
	if err := rows.Err(); err != nil {
		return httperror.Internal(err)
	}
	for _, h := range list {
		if _, err := p.pool.Exec(ctx, `DELETE FROM employment_drafts WHERE id = $1 AND state = 'draft'`, h.id); err != nil {
			return httperror.Internal(err)
		}
		_ = p.audit.Record(ctx, "system", "onboarding.auto_delete", "employment_draft", h.id, map[string]any{
			"title": h.title, "days": days,
		})
	}
	return nil
}
