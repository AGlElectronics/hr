package service

import (
	"context"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AGlElectronics/hr/backend/internal/audit"
	"github.com/AGlElectronics/hr/backend/internal/httperror"
	"github.com/google/uuid"
)

// Settings are HR-wide configuration values editable in the UI.
type Settings struct {
	// OrgEmailDomain is appended to organization emails ("name" -> "name@domain").
	OrgEmailDomain string `json:"org_email_domain"`
}

// SettingsService stores Settings as key/value rows.
type SettingsService struct {
	pool  *pgxpool.Pool
	audit *audit.Writer
}

// NewSettingsService wires a SettingsService.
func NewSettingsService(pool *pgxpool.Pool, aud *audit.Writer) *SettingsService {
	return &SettingsService{pool: pool, audit: aud}
}

var domainRE = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]*[a-z0-9])?\.)+[a-z]{2,}$`)

// Get returns the current settings (empty values when unset).
func (s *SettingsService) Get(ctx context.Context) (*Settings, error) {
	rows, err := s.pool.Query(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return nil, httperror.Internal(err)
	}
	defer rows.Close()
	out := &Settings{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, httperror.Internal(err)
		}
		if k == "org_email_domain" {
			out.OrgEmailDomain = v
		}
	}
	return out, rows.Err()
}

// Update validates and stores the settings.
func (s *SettingsService) Update(ctx context.Context, actor string, in Settings) (*Settings, error) {
	in.OrgEmailDomain = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(in.OrgEmailDomain), "@"))
	if in.OrgEmailDomain != "" && !domainRE.MatchString(in.OrgEmailDomain) {
		return nil, httperror.Validation("validation failed", map[string]string{"org_email_domain": "must be a domain like example.org"})
	}
	if _, err := s.pool.Exec(ctx, `
INSERT INTO settings (key, value, updated_at) VALUES ('org_email_domain', $1, now())
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`, in.OrgEmailDomain); err != nil {
		return nil, httperror.Internal(err)
	}
	if err := s.audit.Record(ctx, actor, "settings.update", "settings", uuid.Nil, map[string]any{
		"org_email_domain": in.OrgEmailDomain,
	}); err != nil {
		return nil, err
	}
	return s.Get(ctx)
}
