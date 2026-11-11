package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"

	"github.com/AGlElectronics/hr/backend/internal/config"
	"github.com/AGlElectronics/hr/backend/internal/groupmatch"
)

// OIDCVerifier checks the access tokens the web app sends (token contract,
// docs/concepts/authentication.md): signature, issuer, expiry and an
// audience naming HR's API (OIDC_AUDIENCE, default hr-api). ID tokens are
// refused.
type OIDCVerifier struct {
	verifier *oidc.IDTokenVerifier
	issuer   string
	groups   groupmatch.Config
}

// Claims is the subset HR uses for actor stamping and group checks.
type Claims struct {
	Issuer            string `json:"iss"`
	Subject           string `json:"sub"`
	Email             string `json:"email"`
	EmailVerified     bool   `json:"email_verified"`
	PreferredUsername string `json:"preferred_username"`
	Name              string `json:"name"`
	// Type is Keycloak's "typ" claim: "Bearer" for access tokens, "ID" for
	// ID tokens.
	Type string `json:"typ"`
	// Groups come only from OIDC_GROUPS_CLAIM (groupmatch); "roles" is
	// never merged in.
	Groups []string `json:"-"`
}

// NewOIDCVerifier discovers the issuer and builds a verifier for HR's API
// audience.
func NewOIDCVerifier(ctx context.Context, cfg *config.Config) (*OIDCVerifier, error) {
	if cfg.OIDCIssuerURL == "" || cfg.OIDCClientID == "" {
		return nil, errors.New("OIDC verifier requires OIDC_ISSUER_URL and OIDC_CLIENT_ID")
	}
	provider, err := oidc.NewProvider(ctx, cfg.OIDCIssuerURL)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery: %w", err)
	}
	return &OIDCVerifier{
		verifier: provider.Verifier(&oidc.Config{ClientID: cfg.Audience()}),
		issuer:   cfg.OIDCIssuerURL,
		groups:   cfg.GroupMatch,
	}, nil
}

// Verify validates a bearer access token and returns its claims. The issuer
// is the configured one, so downstream code never trusts a client value.
func (v *OIDCVerifier) Verify(ctx context.Context, bearer string) (*Claims, error) {
	bearer = strings.TrimSpace(bearer)
	if bearer == "" {
		return nil, errors.New("missing bearer token")
	}
	tok, err := v.verifier.Verify(ctx, bearer)
	if err != nil {
		return nil, fmt.Errorf("verify token: %w", err)
	}
	var c Claims
	if err := tok.Claims(&c); err != nil {
		return nil, fmt.Errorf("parse claims: %w", err)
	}
	if strings.EqualFold(c.Type, "ID") {
		return nil, errors.New("an ID token was sent; the API needs an access token")
	}
	if c.Subject == "" {
		return nil, errors.New("token missing sub claim")
	}
	var all map[string]any
	if err := tok.Claims(&all); err != nil {
		return nil, fmt.Errorf("parse claims: %w", err)
	}
	c.Groups = v.groups.Groups(all)
	c.Issuer = v.issuer
	return &c, nil
}

// DisplayName picks a human label from claims.
func DisplayName(c *Claims) string {
	for _, candidate := range []string{c.PreferredUsername, c.Email, c.Name} {
		if s := strings.TrimSpace(candidate); s != "" {
			return s
		}
	}
	return c.Subject
}
