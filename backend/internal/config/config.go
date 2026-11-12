// Package config loads the HR server configuration from the environment.
package config

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"

	"github.com/AGlElectronics/hr/backend/internal/authbase"
	"github.com/AGlElectronics/hr/backend/internal/denlink"
	"github.com/AGlElectronics/hr/backend/internal/groupmatch"
	"github.com/AGlElectronics/hr/backend/internal/netguard"
)

// AuthMode selects how requests are authenticated (authbase): oidc verifies
// a Bearer access token; dev trusts X-Dev-* headers and is only allowed with
// APP_ENV=development.
type AuthMode = authbase.AuthMode

const (
	AuthModeDev  = authbase.AuthDev
	AuthModeOIDC = authbase.AuthOIDC
)

// DefaultAudience is OIDC_AUDIENCE when unset: the audience Keycloak adds to
// access tokens for HR's API (token contract).
const DefaultAudience = "hr-api"

// MinServiceTokenLength is the shortest HR_SERVICE_TOKEN HR accepts; the
// modules that check it refuse shorter ones too.
const MinServiceTokenLength = 32

// Provisioner modes (IDP_PROVISIONER_MODE).
const (
	// ProvisionerManage creates, renames, disables and deletes accounts and
	// creates the groups HR assigns (HR owns the realm).
	ProvisionerManage = "manage"
	// ProvisionerLink only finds existing accounts and manages membership of
	// HR's groups; it never creates, renames or deletes accounts or groups.
	ProvisionerLink = "link"
	// ProvisionerOff keeps records in HR only; no IDP is touched.
	ProvisionerOff = "off"
)

// knownDevSecrets are values shipped in the development Compose files and
// examples. Outside APP_ENV=development the backend refuses to start with any
// of them, so a copied dev file never reaches production.
var knownDevSecrets = []string{
	"hr-provisioner-dev-secret",
	"learn-service-dev-token",
	"wiki-service-dev-token",
	"tasks-service-dev-token",
	"hr-directory-dev-token",
	"dev-only-hr-service-token-change-me-0000000000",
	"conservicesdev",
	"change-me",
}

type Config struct {
	// AppEnv defaults to production; only "development" relaxes rules.
	AppEnv      string
	AppBaseURL  string
	HTTPAddr    string
	DatabaseURL string

	// S3 is LOCAL DEV ONLY in this scaffold (SeaweedFS).
	S3Endpoint        string
	S3Bucket          string
	S3AccessKeyID     string
	S3SecretAccessKey string
	S3Region          string
	S3UsePathStyle    bool

	AuthMode       AuthMode
	DevDefaultUser string

	OIDCIssuerURL string
	OIDCClientID  string
	OIDCAudience  string
	// AdminGroups, ViewerGroups and LeadGroups are the IDP groups that grant
	// the HR roles admin, viewer (read-only) and lead (department lead).
	// They are compared with GroupMatch, never with ==.
	AdminGroups  []string
	ViewerGroups []string
	LeadGroups   []string
	// GroupMatch is OIDC_GROUPS_CLAIM, OIDC_GROUP_MATCH and OIDC_GROUP_PREFIX.
	GroupMatch groupmatch.Config
	// HRServiceToken is sent when HR fetches the role catalogs of other
	// modules (HR_SERVICE_TOKEN), and accepted on HR's own catalog. Every
	// module that checks it must have the same value.
	HRServiceToken string

	// ProvisionerMode is IDP_PROVISIONER_MODE: manage, link or off.
	ProvisionerMode string

	// Keycloak Admin API (IDP provisioner). Required in manage and link mode
	// outside development.
	KeycloakBaseURL      string
	KeycloakRealm        string
	KeycloakClientID     string
	KeycloakClientSecret string
	// Where the set-password email link sends the user afterwards.
	KeycloakSetupClientID    string
	KeycloakSetupRedirectURI string
	// KeycloakSendPasswordSetup emails new accounts a set-password link
	// (needs SMTP on the realm). Off for brokered accounts, which sign in
	// through the convention's SSO and have no local password.
	KeycloakSendPasswordSetup bool
	// KeycloakGroupParent / KeycloakGroupPrefix place HR's groups: full path
	// <parent>/<prefix><name>, e.g. /conservices/dept-security.
	KeycloakGroupParent string
	KeycloakGroupPrefix string
	// KeycloakUserAttributes are the attributes HR may write (only those the
	// realm's user profile declares are written).
	KeycloakUserAttributes []string

	// JobInterval is how often the job worker looks for due jobs (it also
	// wakes when a job is queued). MaintenanceInterval runs retention, purges,
	// stuck-onboarding recovery and Workspace clean-up. ReviewScanInterval
	// scans the IDP for accounts without an HR record (0: off).
	JobInterval         time.Duration
	MaintenanceInterval time.Duration
	ReviewScanInterval  time.Duration

	// RoleCatalogSources: "stash=http://…/api/role-catalog,events=…".
	RoleCatalogSources string

	// QualificationCatalogSources: "learn=http://…/api/qualification-catalog".
	QualificationCatalogSources string
	// LearnHoldingsURL + LearnServiceToken let HR ask Learn who holds which
	// qualification. Role gating is off when either is empty.
	LearnHoldingsURL  string
	LearnServiceToken string

	// OffboardHooks are modules told when someone is offboarded, as
	// comma-separated "url|token" entries (e.g. the wiki orphaning the
	// person's private space). Failures become warnings, never blockers.
	OffboardHooks string
	// DepartmentHooks are modules told when a department is created or
	// renamed (e.g. Tasks, which keeps one space per department), in the
	// same comma-separated "url|token" format as OffboardHooks.
	DepartmentHooks string
	// DirectoryToken lets other modules (the wiki's share dialog) list HR's
	// departments and access groups at GET /api/internal/directory/groups.
	// Empty turns the endpoint off.
	DirectoryToken string
	// GoogleWorkspaceCredentialsFile is the service-account key (JSON) the
	// Google Workspace connector uses. Empty keeps the connector unavailable.
	GoogleWorkspaceCredentialsFile string
	// RoleGateInterval is how often pending role grants are re-checked.
	RoleGateInterval time.Duration

	// SMTP for HR's own notices (e.g. "your account was deleted"). Empty
	// host disables them; HR then shows a warning instead.
	SMTPHost     string
	SMTPPort     int
	SMTPUsername string
	SMTPPassword string
	SMTPFrom     string
	SMTPFromName string

	// Den is the pairing configuration (DEN_MODE, DEN_INTERNAL_URL,
	// DEN_ALLOWED_HOSTS, DEN_LINK_ENCRYPTION_KEY[_PREVIOUS], APP_ENV).
	Den denlink.Config

	// OutboundAllowedHosts are host names or CIDRs the address lookup may
	// reach even though they are internal (e.g. a self-hosted Photon).
	OutboundAllowedHosts []string

	CORSAllowedOrigins []string
	// TrustedProxies are the proxies whose X-Forwarded-For entries are
	// believed when working out the client IP (TRUSTED_PROXY_CIDRS). The
	// zero value trusts none.
	TrustedProxies authbase.TrustedProxies

	ShutdownTimeout time.Duration
}

func Load() (*Config, error) {
	_ = godotenv.Load()

	var errs []string
	auth, err := authbase.FromEnv(os.Getenv)
	if err != nil {
		errs = append(errs, err.Error())
	}
	groups, err := groupmatch.FromEnv(os.Getenv)
	if err != nil {
		errs = append(errs, err.Error())
	}
	den, err := denlink.FromEnv("hr", "", os.Getenv)
	if err != nil {
		errs = append(errs, err.Error())
	}

	cfg := &Config{
		AppEnv:                         auth.AppEnv,
		AppBaseURL:                     getEnv("APP_BASE_URL", "http://localhost:8080"),
		HTTPAddr:                       getEnv("HTTP_ADDR", ":8080"),
		DatabaseURL:                    getEnv("DATABASE_URL", ""),
		S3Endpoint:                     getEnv("S3_ENDPOINT", ""),
		S3Bucket:                       getEnv("S3_BUCKET", "hr-data-dev"),
		S3AccessKeyID:                  getEnv("S3_ACCESS_KEY_ID", ""),
		S3SecretAccessKey:              getEnv("S3_SECRET_ACCESS_KEY", ""),
		S3Region:                       getEnv("S3_REGION", "us-east-1"),
		S3UsePathStyle:                 getEnvBool("S3_USE_PATH_STYLE", true),
		AuthMode:                       auth.AuthMode,
		DevDefaultUser:                 getEnv("DEV_DEFAULT_USER", "dev-hr-admin"),
		OIDCIssuerURL:                  getEnv("OIDC_ISSUER_URL", ""),
		OIDCClientID:                   getEnv("OIDC_CLIENT_ID", ""),
		OIDCAudience:                   getEnv("OIDC_AUDIENCE", DefaultAudience),
		AdminGroups:                    splitCSV(getEnv("HR_ADMIN_GROUPS", "hr-admin")),
		ViewerGroups:                   splitCSV(getEnv("HR_VIEWER_GROUPS", "hr-viewer")),
		LeadGroups:                     splitCSV(getEnv("HR_LEAD_GROUPS", "hr-lead")),
		GroupMatch:                     groups,
		HRServiceToken:                 strings.TrimSpace(getEnv("HR_SERVICE_TOKEN", "")),
		ProvisionerMode:                strings.ToLower(getEnv("IDP_PROVISIONER_MODE", ProvisionerManage)),
		KeycloakBaseURL:                getEnv("KEYCLOAK_BASE_URL", ""),
		KeycloakRealm:                  getEnv("KEYCLOAK_REALM", "conservices"),
		KeycloakClientID:               getEnv("KEYCLOAK_CLIENT_ID", "hr-provisioner"),
		KeycloakClientSecret:           getEnv("KEYCLOAK_CLIENT_SECRET", ""),
		KeycloakSetupClientID:          getEnv("KEYCLOAK_SETUP_CLIENT_ID", ""),
		KeycloakSetupRedirectURI:       getEnv("KEYCLOAK_SETUP_REDIRECT_URI", ""),
		KeycloakSendPasswordSetup:      getEnvBool("KEYCLOAK_SEND_PASSWORD_SETUP", true),
		KeycloakGroupParent:            getEnv("KEYCLOAK_GROUP_PARENT", ""),
		KeycloakGroupPrefix:            getEnv("KEYCLOAK_GROUP_PREFIX", ""),
		KeycloakUserAttributes:         splitCSV(getEnv("KEYCLOAK_USER_ATTRIBUTES", "hrPersonId,nickname,orgEmail")),
		JobInterval:                    getEnvDuration("JOB_INTERVAL", 10*time.Second),
		MaintenanceInterval:            getEnvDuration("MAINTENANCE_INTERVAL", time.Hour),
		ReviewScanInterval:             getEnvDuration("REVIEW_SCAN_INTERVAL", 15*time.Minute),
		RoleCatalogSources:             getEnv("ROLE_CATALOG_SOURCES", ""),
		QualificationCatalogSources:    getEnv("QUALIFICATION_CATALOG_SOURCES", ""),
		LearnHoldingsURL:               getEnv("LEARN_HOLDINGS_URL", ""),
		LearnServiceToken:              getEnv("LEARN_SERVICE_TOKEN", ""),
		OffboardHooks:                  getEnv("OFFBOARD_HOOKS", ""),
		DepartmentHooks:                getEnv("DEPARTMENT_HOOKS", ""),
		DirectoryToken:                 getEnv("HR_DIRECTORY_TOKEN", ""),
		GoogleWorkspaceCredentialsFile: getEnv("GOOGLE_WORKSPACE_CREDENTIALS_FILE", ""),
		RoleGateInterval:               getEnvDuration("ROLE_GATE_INTERVAL", time.Minute),
		SMTPHost:                       getEnv("SMTP_HOST", ""),
		SMTPPort:                       getEnvInt("SMTP_PORT", 587),
		SMTPUsername:                   getEnv("SMTP_USERNAME", ""),
		SMTPPassword:                   getEnv("SMTP_PASSWORD", ""),
		SMTPFrom:                       getEnv("SMTP_FROM", ""),
		SMTPFromName:                   getEnv("SMTP_FROM_NAME", ""),
		Den:                            den,
		OutboundAllowedHosts:           splitCSV(getEnv("OUTBOUND_ALLOWED_HOSTS", "")),
		CORSAllowedOrigins:             splitCSV(getEnv("CORS_ALLOWED_ORIGINS", "http://localhost:3003")),
		TrustedProxies:                 auth.TrustedProxies,
		ShutdownTimeout:                getEnvDuration("SHUTDOWN_TIMEOUT", 15*time.Second),
	}

	if err := cfg.validate(errs); err != nil {
		return nil, err
	}
	return cfg, nil
}

// validate adds the HR-specific checks to errs (from the shared packages'
// FromEnv) and returns them all at once.
func (c *Config) validate(errs []string) error {
	if c.DatabaseURL == "" {
		errs = append(errs, "DATABASE_URL is required")
	}
	if c.HRServiceToken != "" && len(c.HRServiceToken) < MinServiceTokenLength {
		errs = append(errs, fmt.Sprintf("HR_SERVICE_TOKEN must be at least %d characters (openssl rand -hex 32)", MinServiceTokenLength))
	}
	// AUTH_MODE=dev outside development is refused by authbase.FromEnv.
	if c.AuthMode == AuthModeOIDC {
		if c.OIDCIssuerURL == "" {
			errs = append(errs, "OIDC_ISSUER_URL is required when AUTH_MODE=oidc")
		}
		if c.OIDCClientID == "" {
			errs = append(errs, "OIDC_CLIENT_ID is required when AUTH_MODE=oidc")
		}
	}
	switch c.ProvisionerMode {
	case ProvisionerManage, ProvisionerLink, ProvisionerOff:
	default:
		errs = append(errs, "IDP_PROVISIONER_MODE must be 'manage', 'link' or 'off'")
	}
	if c.ProvisionerMode != ProvisionerOff && !c.KeycloakConfigured() && !c.IsDevelopment() {
		errs = append(errs, "IDP_PROVISIONER_MODE="+c.ProvisionerMode+" needs KEYCLOAK_BASE_URL and KEYCLOAK_CLIENT_SECRET "+
			"(set IDP_PROVISIONER_MODE=off to keep records in HR only)")
	}
	if !c.IsDevelopment() {
		errs = append(errs, c.devSecretsInUse()...)
	}
	if c.DirectoryToken != "" && len(c.DirectoryToken) < 16 {
		errs = append(errs, "HR_DIRECTORY_TOKEN must be at least 16 characters")
	}
	if _, err := netguard.New(c.OutboundAllowedHosts, false); err != nil {
		errs = append(errs, err.Error())
	}
	kcAny := c.KeycloakBaseURL != "" || c.KeycloakClientSecret != ""
	if kcAny {
		if c.KeycloakBaseURL == "" {
			errs = append(errs, "KEYCLOAK_BASE_URL is required when Keycloak provisioning is enabled")
		}
		if c.KeycloakClientSecret == "" {
			errs = append(errs, "KEYCLOAK_CLIENT_SECRET is required when Keycloak provisioning is enabled")
		}
	}
	s3Any := c.S3Endpoint != "" || c.S3AccessKeyID != "" || c.S3SecretAccessKey != ""
	if s3Any {
		if c.S3Endpoint == "" {
			errs = append(errs, "S3_ENDPOINT is required when any S3_* var is set")
		}
		if c.S3Bucket == "" {
			errs = append(errs, "S3_BUCKET is required when any S3_* var is set")
		}
		if c.S3AccessKeyID == "" {
			errs = append(errs, "S3_ACCESS_KEY_ID is required when any S3_* var is set")
		}
		if c.S3SecretAccessKey == "" {
			errs = append(errs, "S3_SECRET_ACCESS_KEY is required when any S3_* var is set")
		}
	}
	if len(errs) > 0 {
		return errors.New("invalid config: " + strings.Join(errs, "; "))
	}
	return nil
}

// IsDevelopment reports APP_ENV=development.
func (c *Config) IsDevelopment() bool { return c.Auth().Development() }

// Auth is the authbase view of AUTH_MODE, APP_ENV and TRUSTED_PROXY_CIDRS.
func (c *Config) Auth() authbase.Config {
	return authbase.Config{AppEnv: c.AppEnv, AuthMode: c.AuthMode, TrustedProxies: c.TrustedProxies}
}

// Audience is the audience access tokens must carry.
func (c *Config) Audience() string {
	if c.OIDCAudience != "" {
		return c.OIDCAudience
	}
	return DefaultAudience
}

// DenConfig is Den with the slug and version filled in, and APP_ENV taken
// from the process when Den does not set its own.
func (c *Config) DenConfig(version string) denlink.Config {
	d := c.Den
	d.Slug, d.Version = "hr", version
	if d.AppEnv == "" {
		d.AppEnv = c.AppEnv
	}
	return d
}

// devSecretsInUse lists every secret-bearing variable that holds a known
// development value.
func (c *Config) devSecretsInUse() []string {
	secrets := map[string]string{
		"KEYCLOAK_CLIENT_SECRET": c.KeycloakClientSecret,
		"LEARN_SERVICE_TOKEN":    c.LearnServiceToken,
		"HR_DIRECTORY_TOKEN":     c.DirectoryToken,
		"HR_SERVICE_TOKEN":       c.HRServiceToken,
		"SMTP_PASSWORD":          c.SMTPPassword,
		"S3_SECRET_ACCESS_KEY":   c.S3SecretAccessKey,
	}
	for name, raw := range map[string]string{"OFFBOARD_HOOKS": c.OffboardHooks, "DEPARTMENT_HOOKS": c.DepartmentHooks} {
		for i, entry := range strings.Split(raw, ",") {
			if _, token, ok := strings.Cut(entry, "|"); ok {
				secrets[fmt.Sprintf("%s (entry %d)", name, i+1)] = strings.TrimSpace(token)
			}
		}
	}
	var out []string
	for name, v := range secrets {
		for _, dev := range knownDevSecrets {
			if v != "" && v == dev {
				out = append(out, name+" uses the development value "+strconv.Quote(dev)+"; set a real secret (APP_ENV="+c.AppEnv+")")
			}
		}
	}
	sort.Strings(out)
	return out
}

// KeycloakConfigured reports whether real IDP provisioning should be wired.
func (c *Config) KeycloakConfigured() bool {
	return c.KeycloakBaseURL != "" && c.KeycloakClientSecret != ""
}

// RoleGateConfigured reports whether HR can ask Learn for qualification
// holdings, i.e. whether training requirements are enforced.
func (c *Config) RoleGateConfigured() bool {
	return c.LearnHoldingsURL != "" && c.LearnServiceToken != ""
}

func (c *Config) S3Configured() bool {
	return c.S3Endpoint != "" && c.S3Bucket != "" && c.S3AccessKeyID != "" && c.S3SecretAccessKey != ""
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func getEnvBool(key string, fallback bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return parsed
}

func getEnvInt(key string, fallback int) int {
	v, err := strconv.Atoi(os.Getenv(key))
	if err != nil {
		return fallback
	}
	return v
}

func getEnvDuration(key string, fallback time.Duration) time.Duration {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return parsed
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (c *Config) String() string {
	return fmt.Sprintf(
		"env=%s addr=%s auth=%s db=%s bucket=%s keycloak=%v cors=%v",
		c.AppEnv, c.HTTPAddr, c.AuthMode, maskDSN(c.DatabaseURL), c.S3Bucket, c.KeycloakConfigured(), c.CORSAllowedOrigins,
	)
}

func maskDSN(dsn string) string {
	if dsn == "" {
		return ""
	}
	at := strings.LastIndex(dsn, "@")
	colon := strings.Index(dsn, "://")
	if at > 0 && colon > 0 && at > colon+3 {
		return dsn[:colon+3] + "***:***" + dsn[at:]
	}
	return dsn
}
