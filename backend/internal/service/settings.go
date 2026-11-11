package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
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
	// DefaultCountry (ISO 3166-1 alpha-2) is assumed for phone numbers typed
	// without a country code and preferred in address suggestions. HR-only.
	DefaultCountry string `json:"default_country"`
	// AddressLookupURL is a Photon (OpenStreetMap) server for address
	// suggestions; empty turns suggestions off. HR-only.
	AddressLookupURL string `json:"address_lookup_url"`
	// AdminNotificationEmails receive HR's notices to admins (retention and
	// Workspace deletion warnings). Empty: the members of the HR admin groups.
	AdminNotificationEmails string `json:"admin_notification_emails"`
	// Identity describes the convention's sign-in setup (HR overrides Den).
	Identity IdentitySettings `json:"identity"`
	// Retention is how long HR keeps personal data.
	Retention RetentionSettings `json:"retention"`
	// Workspace is the Google Workspace connector.
	Workspace WorkspaceSettings `json:"workspace"`
	// ManagedBy is "den" while HR is paired with Den: the organisation fields
	// are read-only here, the HR-only fields stay editable.
	ManagedBy string `json:"managed_by,omitempty"`
}

// IdentitySettings are the identity links HR shows. Each field set here
// overrides the value Den passes to paired modules.
type IdentitySettings struct {
	// StaffMode is own, brokered or existing (see the existing identity
	// provider page). In brokered and existing mode the offboarding screen
	// warns that the convention's SSO account stays active.
	StaffMode string `json:"staff_mode"`
	// SSOName and SSOURL name and link the convention's SSO in that warning.
	SSOName string `json:"sso_name"`
	SSOURL  string `json:"sso_url"`
	// AdminConsoleURL is the IdP admin console ("Open Keycloak").
	AdminConsoleURL string `json:"admin_console_url"`
	// Effective values after applying Den's settings (read-only).
	Effective *IdentitySettings `json:"effective,omitempty"`
}

// RetentionSettings say how long HR keeps personal data. The defaults are
// pending legal review.
type RetentionSettings struct {
	// Enabled turns the automatic purge on.
	Enabled bool `json:"enabled"`
	// ApplicantMonths: applicants untouched this long are deleted.
	ApplicantMonths int `json:"applicant_months"`
	// PausedMonths: people paused this long are offboarded.
	PausedMonths int `json:"paused_months"`
	// OffboardedAuditMonths: access history kept after an offboarding.
	OffboardedAuditMonths int `json:"offboarded_audit_months"`
	// WarningDays: HR admins are emailed this many days before a purge.
	WarningDays int `json:"warning_days"`
	// ManagedBy is "den" when Den's module settings set retention.
	ManagedBy string `json:"managed_by,omitempty"`
}

// DefaultRetention are the retention defaults (pending legal review).
var DefaultRetention = RetentionSettings{Enabled: true, ApplicantMonths: 6, PausedMonths: 24, OffboardedAuditMonths: 12, WarningDays: 14}

// WorkspaceSettings configure the Google Workspace connector.
type WorkspaceSettings struct {
	Enabled bool `json:"enabled"`
	// Domain of the Workspace accounts; empty uses the organization email domain.
	Domain string `json:"domain"`
	// AdminEmail is the Workspace admin the service account acts as
	// (domain-wide delegation).
	AdminEmail string `json:"admin_email"`
	// OrgUnit is where new accounts go, e.g. "/Volunteers".
	OrgUnit string `json:"org_unit"`
	// DeleteAfterDays: suspended accounts of offboarded people are deleted
	// this many days after the offboarding.
	DeleteAfterDays int `json:"delete_after_days"`
	// ReminderDays: HR admins are emailed this many days before a deletion.
	ReminderDays int `json:"reminder_days"`
	// Available is false while HR has no Workspace client (read-only).
	Available bool `json:"available"`
}

// DefaultWorkspace are the Workspace defaults.
var DefaultWorkspace = WorkspaceSettings{OrgUnit: "/", DeleteAfterDays: 30, ReminderDays: 7}

// DefaultAddressLookupURL is the public Photon instance run by komoot (fair
// use; addresses typed into HR are sent there, see the privacy notice).
const DefaultAddressLookupURL = "https://photon.komoot.io"

// hrOnlyKeys are stored in HR even while paired with Den.
var hrOnlyKeys = map[string]bool{
	"default_country": true, "address_lookup_url": true, "admin_notification_emails": true,
	"identity_staff_mode": true, "identity_sso_name": true, "identity_sso_url": true, "identity_admin_console_url": true,
	"workspace_enabled": true, "workspace_domain": true, "workspace_admin_email": true, "workspace_org_unit": true,
	"workspace_delete_after_days": true, "workspace_reminder_days": true,
	"retention_enabled": true, "retention_applicant_months": true, "retention_paused_months": true,
	"retention_offboarded_audit_months": true, "retention_warning_days": true,
}

// OrgSource supplies organisation settings from Den when HR is paired.
type OrgSource interface {
	Paired() bool
	Setting(key string) (string, bool)
}

// SettingsService stores Settings as key/value rows. When HR is paired with
// Den, the organisation fields come from Den instead.
type SettingsService struct {
	pool  *pgxpool.Pool
	audit *audit.Writer
	den   OrgSource
	// workspaceAvailable is true when a Workspace client is wired.
	workspaceAvailable bool
	// addressURLCheck validates the address lookup URL (SSRF guard); nil
	// accepts any http(s) URL.
	addressURLCheck func(raw string) error
}

// NewSettingsService wires a SettingsService; den may be nil (standalone).
func NewSettingsService(pool *pgxpool.Pool, aud *audit.Writer, den OrgSource) *SettingsService {
	return &SettingsService{pool: pool, audit: aud, den: den}
}

// SetWorkspaceAvailable marks the Workspace client as wired.
func (s *SettingsService) SetWorkspaceAvailable(v bool) { s.workspaceAvailable = v }

// SetAddressURLCheck installs the outbound URL guard for the address lookup.
func (s *SettingsService) SetAddressURLCheck(f func(raw string) error) { s.addressURLCheck = f }

// ErrManagedInDen is returned by Update while HR is paired with Den.
var ErrManagedInDen = httperror.New(httperror.CodeConflict, "These settings are managed in Den (Admin → Organisation).")

var domainRE = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]*[a-z0-9])?\.)+[a-z]{2,}$`)

func (s *SettingsService) paired() bool { return s.den != nil && s.den.Paired() }

func atoiOr(v string, def int) int {
	if n, err := strconv.Atoi(v); err == nil {
		return n
	}
	return def
}

// Get returns the current settings; unset values get their defaults.
func (s *SettingsService) Get(ctx context.Context) (*Settings, error) {
	rows, err := s.pool.Query(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return nil, httperror.Internal(err)
	}
	defer rows.Close()
	out := &Settings{UsernameTemplate: DefaultUsernameTemplate, OrgEmailTemplate: DefaultOrgEmailTemplate,
		AddressLookupURL: DefaultAddressLookupURL, Retention: DefaultRetention, Workspace: DefaultWorkspace}
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
		case "default_country":
			out.DefaultCountry = v
		case "address_lookup_url":
			out.AddressLookupURL = v
		case "admin_notification_emails":
			out.AdminNotificationEmails = v
		case "identity_staff_mode":
			out.Identity.StaffMode = v
		case "identity_sso_name":
			out.Identity.SSOName = v
		case "identity_sso_url":
			out.Identity.SSOURL = v
		case "identity_admin_console_url":
			out.Identity.AdminConsoleURL = v
		case "workspace_enabled":
			out.Workspace.Enabled = v == "true"
		case "workspace_domain":
			out.Workspace.Domain = v
		case "workspace_admin_email":
			out.Workspace.AdminEmail = v
		case "workspace_org_unit":
			out.Workspace.OrgUnit = v
		case "workspace_delete_after_days":
			out.Workspace.DeleteAfterDays = atoiOr(v, DefaultWorkspace.DeleteAfterDays)
		case "workspace_reminder_days":
			out.Workspace.ReminderDays = atoiOr(v, DefaultWorkspace.ReminderDays)
		case "retention_enabled":
			out.Retention.Enabled = v == "true"
		case "retention_applicant_months":
			out.Retention.ApplicantMonths = atoiOr(v, DefaultRetention.ApplicantMonths)
		case "retention_paused_months":
			out.Retention.PausedMonths = atoiOr(v, DefaultRetention.PausedMonths)
		case "retention_offboarded_audit_months":
			out.Retention.OffboardedAuditMonths = atoiOr(v, DefaultRetention.OffboardedAuditMonths)
		case "retention_warning_days":
			out.Retention.WarningDays = atoiOr(v, DefaultRetention.WarningDays)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, httperror.Internal(err)
	}
	rows.Close()
	out.Workspace.Available = s.workspaceAvailable
	eff := out.Identity
	if s.paired() {
		out.ManagedBy = "den"
		out.UsernameTemplate, out.OrgEmailTemplate = DefaultUsernameTemplate, DefaultOrgEmailTemplate
		out.OrganizationName, _ = s.den.Setting("organization_name")
		out.OrgEmailDomain, _ = s.den.Setting("org_email_domain")
		if v, _ := s.den.Setting("username_template"); v != "" && ValidateTemplate(v) == "" {
			out.UsernameTemplate = v
		}
		if v, _ := s.den.Setting("org_email_template"); v != "" && ValidateTemplate(v) == "" {
			out.OrgEmailTemplate = v
		}
		for _, f := range []struct {
			key string
			dst *string
		}{{"staff_identity_mode", &eff.StaffMode}, {"sso_name", &eff.SSOName}, {"sso_url", &eff.SSOURL}, {"idp_admin_console_url", &eff.AdminConsoleURL}} {
			if *f.dst == "" {
				*f.dst, _ = s.den.Setting(f.key)
			}
		}
		if r, ok, err := s.denRetention(ctx); err != nil {
			return nil, err
		} else if ok {
			out.Retention = r
		}
	}
	if eff.StaffMode == "" {
		eff.StaffMode = "own"
	}
	out.Identity.Effective = &eff
	return out, nil
}

// denRetention reads the "retention" section Den pushes in the integration
// bundle (Den → Admin → Modules → module settings). Missing fields keep the
// defaults.
func (s *SettingsService) denRetention(ctx context.Context) (RetentionSettings, bool, error) {
	var raw []byte
	err := s.pool.QueryRow(ctx, `SELECT bundle->'retention' FROM den_link WHERE id = 1 AND bundle ? 'retention'`).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) || len(raw) == 0 || string(raw) == "null" {
		return RetentionSettings{}, false, nil
	}
	if err != nil {
		return RetentionSettings{}, false, httperror.Internal(err)
	}
	r := DefaultRetention
	if err := json.Unmarshal(raw, &r); err != nil {
		return RetentionSettings{}, false, nil
	}
	r.ManagedBy = "den"
	return r, true, nil
}

var emailListRE = regexp.MustCompile(`^[^@\s,]+@[^@\s,]+\.[^@\s,]+$`)

// Normalize trims and validates settings, returning per-field errors.
func (in *Settings) Normalize() map[string]string {
	in.OrganizationName = strings.TrimSpace(in.OrganizationName)
	in.OrgEmailDomain = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(in.OrgEmailDomain), "@"))
	in.UsernameTemplate = strings.TrimSpace(in.UsernameTemplate)
	in.OrgEmailTemplate = strings.TrimSpace(in.OrgEmailTemplate)
	in.DefaultCountry = strings.ToUpper(strings.TrimSpace(in.DefaultCountry))
	in.AddressLookupURL = strings.TrimRight(strings.TrimSpace(in.AddressLookupURL), "/")
	fields := map[string]string{}
	if in.DefaultCountry != "" && !countryRE.MatchString(in.DefaultCountry) {
		fields["default_country"] = "must be a two-letter country code, e.g. AT"
	}
	if in.AddressLookupURL != "" {
		if u, err := url.Parse(in.AddressLookupURL); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			fields["address_lookup_url"] = "must be an http(s) URL"
		}
	}
	if in.OrgEmailDomain != "" && !domainRE.MatchString(in.OrgEmailDomain) {
		fields["org_email_domain"] = "must be a domain like example.org"
	}
	if msg := ValidateTemplate(in.UsernameTemplate); msg != "" {
		fields["username_template"] = msg
	}
	if msg := ValidateTemplate(in.OrgEmailTemplate); msg != "" {
		fields["org_email_template"] = msg
	}
	var emails []string
	for _, e := range strings.Split(in.AdminNotificationEmails, ",") {
		if e = strings.ToLower(strings.TrimSpace(e)); e != "" {
			if !emailListRE.MatchString(e) {
				fields["admin_notification_emails"] = "comma-separated email addresses"
			}
			emails = append(emails, e)
		}
	}
	in.AdminNotificationEmails = strings.Join(emails, ",")

	id := &in.Identity
	id.StaffMode, id.SSOName = strings.TrimSpace(id.StaffMode), strings.TrimSpace(id.SSOName)
	id.SSOURL, id.AdminConsoleURL = strings.TrimSpace(id.SSOURL), strings.TrimSpace(id.AdminConsoleURL)
	id.Effective = nil
	switch id.StaffMode {
	case "", "own", "brokered", "existing":
	default:
		fields["identity.staff_mode"] = "own, brokered or existing"
	}
	for name, v := range map[string]string{"identity.sso_url": id.SSOURL, "identity.admin_console_url": id.AdminConsoleURL} {
		if v != "" {
			if u, err := url.Parse(v); err != nil || u.Scheme != "https" && u.Scheme != "http" || u.Host == "" {
				fields[name] = "must be an http(s) URL"
			}
		}
	}

	ws := &in.Workspace
	ws.Domain = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(ws.Domain), "@"))
	ws.AdminEmail = strings.ToLower(strings.TrimSpace(ws.AdminEmail))
	ws.OrgUnit = strings.TrimSpace(ws.OrgUnit)
	if ws.OrgUnit == "" {
		ws.OrgUnit = "/"
	}
	if ws.Domain != "" && !domainRE.MatchString(ws.Domain) {
		fields["workspace.domain"] = "must be a domain like example.org"
	}
	if ws.AdminEmail != "" && !emailListRE.MatchString(ws.AdminEmail) {
		fields["workspace.admin_email"] = "must be an email address"
	}
	if !strings.HasPrefix(ws.OrgUnit, "/") {
		fields["workspace.org_unit"] = `must start with "/", e.g. /Volunteers`
	}
	if ws.Enabled && ws.AdminEmail == "" {
		fields["workspace.admin_email"] = "required when the Workspace connector is on"
	}
	if ws.DeleteAfterDays < 1 {
		fields["workspace.delete_after_days"] = "at least 1 day"
	}
	if ws.ReminderDays < 0 || ws.ReminderDays >= ws.DeleteAfterDays {
		fields["workspace.reminder_days"] = "0 or more, and fewer than the days before deletion"
	}

	rt := &in.Retention
	rt.ManagedBy = ""
	for name, v := range map[string]int{"retention.applicant_months": rt.ApplicantMonths, "retention.paused_months": rt.PausedMonths,
		"retention.offboarded_audit_months": rt.OffboardedAuditMonths} {
		if v < 1 || v > 120 {
			fields[name] = "between 1 and 120 months"
		}
	}
	if rt.WarningDays < 1 || rt.WarningDays > 90 {
		fields["retention.warning_days"] = "between 1 and 90 days"
	}
	return fields
}

// Update validates and stores the settings. While paired with Den only the
// HR-only fields are saved (and retention only when Den does not set it).
func (s *SettingsService) Update(ctx context.Context, actor string, in Settings) (*Settings, error) {
	current, err := s.Get(ctx)
	if err != nil {
		return nil, err
	}
	denRetention := current.Retention.ManagedBy == "den"
	if denRetention {
		in.Retention = current.Retention
	}
	fields := in.Normalize()
	if s.paired() {
		for k := range fields {
			if !hrOnlyKeys[k] && !strings.Contains(k, ".") && k != "admin_notification_emails" {
				delete(fields, k)
			}
		}
	}
	if fields["address_lookup_url"] == "" && in.AddressLookupURL != "" && s.addressURLCheck != nil {
		if err := s.addressURLCheck(in.AddressLookupURL); err != nil {
			fields["address_lookup_url"] = err.Error()
		}
	}
	if len(fields) > 0 {
		return nil, httperror.Validation("validation failed", fields)
	}
	b := strconv.FormatBool
	i := strconv.Itoa
	values := map[string]string{
		"organization_name":           in.OrganizationName,
		"org_email_domain":            in.OrgEmailDomain,
		"username_template":           in.UsernameTemplate,
		"org_email_template":          in.OrgEmailTemplate,
		"default_country":             in.DefaultCountry,
		"address_lookup_url":          in.AddressLookupURL,
		"admin_notification_emails":   in.AdminNotificationEmails,
		"identity_staff_mode":         in.Identity.StaffMode,
		"identity_sso_name":           in.Identity.SSOName,
		"identity_sso_url":            in.Identity.SSOURL,
		"identity_admin_console_url":  in.Identity.AdminConsoleURL,
		"workspace_enabled":           b(in.Workspace.Enabled),
		"workspace_domain":            in.Workspace.Domain,
		"workspace_admin_email":       in.Workspace.AdminEmail,
		"workspace_org_unit":          in.Workspace.OrgUnit,
		"workspace_delete_after_days": i(in.Workspace.DeleteAfterDays),
		"workspace_reminder_days":     i(in.Workspace.ReminderDays),
	}
	if !denRetention {
		values["retention_enabled"] = b(in.Retention.Enabled)
		values["retention_applicant_months"] = i(in.Retention.ApplicantMonths)
		values["retention_paused_months"] = i(in.Retention.PausedMonths)
		values["retention_offboarded_audit_months"] = i(in.Retention.OffboardedAuditMonths)
		values["retention_warning_days"] = i(in.Retention.WarningDays)
	}
	for k, v := range values {
		if s.paired() && !hrOnlyKeys[k] {
			continue
		}
		if _, err := s.pool.Exec(ctx, `
INSERT INTO settings (key, value, updated_at) VALUES ($1, $2, now())
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`, k, v); err != nil {
			return nil, httperror.Internal(err)
		}
	}
	if err := s.audit.Record(ctx, actor, "settings.update", "settings", uuid.Nil, values); err != nil {
		return nil, err
	}
	return s.Get(ctx)
}
