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
	// OrganizationName is used in emails HR sends ("… at <name>").
	OrganizationName string `json:"organization_name"`
	// OrgEmailDomain is appended to organization emails ("name" -> "name@domain").
	OrgEmailDomain string `json:"org_email_domain"`
	// UsernameTemplate builds the login name of new accounts, e.g. "{nickname}".
	UsernameTemplate string `json:"username_template"`
	// OrgEmailTemplate suggests the part before "@", e.g. "{first}.{last}".
	OrgEmailTemplate string `json:"org_email_template"`
	// ManagedBy is "den" while HR is paired with Den (read-only here).
	ManagedBy string `json:"managed_by,omitempty"`
}

// OrgSource supplies organisation settings from Den when HR is paired.
type OrgSource interface {
	Paired() bool
	Setting(key string) (string, bool)
}

// SettingsService stores Settings as key/value rows. When HR is paired with
// Den, every field comes from Den instead and Update is refused.
type SettingsService struct {
	pool  *pgxpool.Pool
	audit *audit.Writer
	den   OrgSource
}

// NewSettingsService wires a SettingsService; den may be nil (standalone).
func NewSettingsService(pool *pgxpool.Pool, aud *audit.Writer, den OrgSource) *SettingsService {
	return &SettingsService{pool: pool, audit: aud, den: den}
}

// ErrManagedInDen is returned by Update while HR is paired with Den.
var ErrManagedInDen = httperror.New(httperror.CodeConflict, "These settings are managed in Den (Admin → Organisation).")

var domainRE = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]*[a-z0-9])?\.)+[a-z]{2,}$`)

// Get returns the current settings; unset templates get their defaults.
func (s *SettingsService) Get(ctx context.Context) (*Settings, error) {
	if s.den != nil && s.den.Paired() {
		out := &Settings{ManagedBy: "den", UsernameTemplate: DefaultUsernameTemplate, OrgEmailTemplate: DefaultOrgEmailTemplate}
		out.OrganizationName, _ = s.den.Setting("organization_name")
		out.OrgEmailDomain, _ = s.den.Setting("org_email_domain")
		if v, _ := s.den.Setting("username_template"); v != "" && ValidateTemplate(v) == "" {
			out.UsernameTemplate = v
		}
		if v, _ := s.den.Setting("org_email_template"); v != "" && ValidateTemplate(v) == "" {
			out.OrgEmailTemplate = v
		}
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return nil, httperror.Internal(err)
	}
	defer rows.Close()
	out := &Settings{UsernameTemplate: DefaultUsernameTemplate, OrgEmailTemplate: DefaultOrgEmailTemplate}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, httperror.Internal(err)
		}
		switch k {
		case "organization_name":
			out.OrganizationName = v
		case "org_email_domain":
			out.OrgEmailDomain = v
		case "username_template":
			if v != "" {
				out.UsernameTemplate = v
			}
		case "org_email_template":
			if v != "" {
				out.OrgEmailTemplate = v
			}
		}
	}
	return out, rows.Err()
}

// Normalize trims and validates settings, returning per-field errors.
func (in *Settings) Normalize() map[string]string {
	in.OrganizationName = strings.TrimSpace(in.OrganizationName)
	in.OrgEmailDomain = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(in.OrgEmailDomain), "@"))
	in.UsernameTemplate = strings.TrimSpace(in.UsernameTemplate)
	in.OrgEmailTemplate = strings.TrimSpace(in.OrgEmailTemplate)
	fields := map[string]string{}
	if in.OrgEmailDomain != "" && !domainRE.MatchString(in.OrgEmailDomain) {
		fields["org_email_domain"] = "must be a domain like example.org"
	}
	if msg := ValidateTemplate(in.UsernameTemplate); msg != "" {
		fields["username_template"] = msg
	}
	if msg := ValidateTemplate(in.OrgEmailTemplate); msg != "" {
		fields["org_email_template"] = msg
	}
	return fields
}

// Update validates and stores the settings.
func (s *SettingsService) Update(ctx context.Context, actor string, in Settings) (*Settings, error) {
	if s.den != nil && s.den.Paired() {
		return nil, ErrManagedInDen
	}
	if fields := in.Normalize(); len(fields) > 0 {
		return nil, httperror.Validation("validation failed", fields)
	}
	values := map[string]string{
		"organization_name":  in.OrganizationName,
		"org_email_domain":   in.OrgEmailDomain,
		"username_template":  in.UsernameTemplate,
		"org_email_template": in.OrgEmailTemplate,
	}
	for k, v := range values {
		if _, err := s.pool.Exec(ctx, `
INSERT INTO settings (key, value, updated_at) VALUES ($1, $2, now())
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`, k, v); err != nil {
			return nil, httperror.Internal(err)
		}
	}
	if err := s.audit.Record(ctx, actor, "settings.update", "settings", uuid.Nil, map[string]any{
		"organization_name": in.OrganizationName, "org_email_domain": in.OrgEmailDomain, "username_template": in.UsernameTemplate, "org_email_template": in.OrgEmailTemplate,
	}); err != nil {
		return nil, err
	}
	return s.Get(ctx)
}
