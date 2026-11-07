// Package config loads the HR server configuration from the environment.
package config

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"

	"github.com/AGlElectronics/hr/backend/internal/clientip"
	"github.com/AGlElectronics/hr/backend/internal/denlink"
)

const (
	AuthModeDev  = "dev"
	AuthModeOIDC = "oidc"
)

type Config struct {
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

	AuthMode       string
	DevDefaultUser string

	OIDCIssuerURL string
	OIDCClientID  string
	OIDCAudience  string
	// AccessGroups are the IdP groups allowed to use HR at all (OIDC mode).
	AccessGroups []string

	// Keycloak Admin API (IdP provisioner). When BaseURL is set, HR creates
	// real users in the shared realm instead of using FakeIdP.
	KeycloakBaseURL      string
	KeycloakRealm        string
	KeycloakClientID     string
	KeycloakClientSecret string
	// Where the set-password email link sends the user afterwards.
	KeycloakSetupClientID    string
	KeycloakSetupRedirectURI string

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

	// DenInternalURL replaces the host of pasted Den pairing URLs, for when
	// Den is reachable under a different address from this backend (Docker).
	DenInternalURL string
	// DenMode is DEN_MODE: required keeps the API locked until the module
	// is paired with Den; standalone (the default) does not.
	DenMode denlink.Mode

	CORSAllowedOrigins []string
	// TrustedProxyCIDRs lists the proxies whose X-Forwarded-For entries are
	// believed when working out the client IP (TRUSTED_PROXY_CIDRS).
	TrustedProxyCIDRs []netip.Prefix

	ShutdownTimeout time.Duration
}

func Load() (*Config, error) {
	_ = godotenv.Load()

	cfg := &Config{
		AppEnv:                      getEnv("APP_ENV", "development"),
		AppBaseURL:                  getEnv("APP_BASE_URL", "http://localhost:8080"),
		HTTPAddr:                    getEnv("HTTP_ADDR", ":8080"),
		DatabaseURL:                 getEnv("DATABASE_URL", ""),
		S3Endpoint:                  getEnv("S3_ENDPOINT", ""),
		S3Bucket:                    getEnv("S3_BUCKET", "hr-data-dev"),
		S3AccessKeyID:               getEnv("S3_ACCESS_KEY_ID", ""),
		S3SecretAccessKey:           getEnv("S3_SECRET_ACCESS_KEY", ""),
		S3Region:                    getEnv("S3_REGION", "us-east-1"),
		S3UsePathStyle:              getEnvBool("S3_USE_PATH_STYLE", true),
		AuthMode:                    strings.ToLower(getEnv("AUTH_MODE", AuthModeDev)),
		DevDefaultUser:              getEnv("DEV_DEFAULT_USER", "dev-hr-admin"),
		OIDCIssuerURL:               getEnv("OIDC_ISSUER_URL", ""),
		OIDCClientID:                getEnv("OIDC_CLIENT_ID", ""),
		OIDCAudience:                getEnv("OIDC_AUDIENCE", ""),
		AccessGroups:                splitCSV(getEnv("HR_ACCESS_GROUPS", "hr-admin")),
		KeycloakBaseURL:             getEnv("KEYCLOAK_BASE_URL", ""),
		KeycloakRealm:               getEnv("KEYCLOAK_REALM", "conservices"),
		KeycloakClientID:            getEnv("KEYCLOAK_CLIENT_ID", "hr-provisioner"),
		KeycloakClientSecret:        getEnv("KEYCLOAK_CLIENT_SECRET", ""),
		KeycloakSetupClientID:       getEnv("KEYCLOAK_SETUP_CLIENT_ID", ""),
		KeycloakSetupRedirectURI:    getEnv("KEYCLOAK_SETUP_REDIRECT_URI", ""),
		RoleCatalogSources:          getEnv("ROLE_CATALOG_SOURCES", ""),
		QualificationCatalogSources: getEnv("QUALIFICATION_CATALOG_SOURCES", ""),
		LearnHoldingsURL:            getEnv("LEARN_HOLDINGS_URL", ""),
		LearnServiceToken:           getEnv("LEARN_SERVICE_TOKEN", ""),
		OffboardHooks:               getEnv("OFFBOARD_HOOKS", ""),
		DepartmentHooks:             getEnv("DEPARTMENT_HOOKS", ""),
		RoleGateInterval:            getEnvDuration("ROLE_GATE_INTERVAL", time.Minute),
		SMTPHost:                    getEnv("SMTP_HOST", ""),
		SMTPPort:                    getEnvInt("SMTP_PORT", 587),
		SMTPUsername:                getEnv("SMTP_USERNAME", ""),
		SMTPPassword:                getEnv("SMTP_PASSWORD", ""),
		SMTPFrom:                    getEnv("SMTP_FROM", ""),
		SMTPFromName:                getEnv("SMTP_FROM_NAME", ""),
		DenInternalURL:              getEnv("DEN_INTERNAL_URL", ""),
		DenMode:                     denlink.Mode(getEnv("DEN_MODE", string(denlink.ModeStandalone))),
		CORSAllowedOrigins:          splitCSV(getEnv("CORS_ALLOWED_ORIGINS", "http://localhost:3003")),
		ShutdownTimeout:             getEnvDuration("SHUTDOWN_TIMEOUT", 15*time.Second),
	}

	trusted, err := clientip.ParseCIDRs(getEnv("TRUSTED_PROXY_CIDRS", clientip.DefaultTrustedProxies))
	if err != nil {
		return nil, fmt.Errorf("invalid config: TRUSTED_PROXY_CIDRS: %w", err)
	}
	cfg.TrustedProxyCIDRs = trusted

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) validate() error {
	var errs []string
	if c.DatabaseURL == "" {
		errs = append(errs, "DATABASE_URL is required")
	}
	if err := c.DenMode.Validate(); err != nil {
		errs = append(errs, err.Error())
	}
	switch c.AuthMode {
	case AuthModeDev, AuthModeOIDC:
	default:
		errs = append(errs, "AUTH_MODE must be 'dev' or 'oidc'")
	}
	if c.AuthMode == AuthModeOIDC {
		if c.OIDCIssuerURL == "" {
			errs = append(errs, "OIDC_ISSUER_URL is required when AUTH_MODE=oidc")
		}
		if c.OIDCClientID == "" {
			errs = append(errs, "OIDC_CLIENT_ID is required when AUTH_MODE=oidc")
		}
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

// KeycloakConfigured reports whether real IdP provisioning should be wired.
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
