package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"

	"github.com/AGlElectronics/hr/backend/internal/config"
)

// OIDCVerifier verifies Bearer ID tokens from the shared Keycloak realm.
type OIDCVerifier struct {
	verifier *oidc.IDTokenVerifier
	issuer   string
}

// Claims is the subset HR uses for actor stamping and optional group checks.
type Claims struct {
	Issuer            string   `json:"iss"`
	Subject           string   `json:"sub"`
	Email             string   `json:"email"`
	EmailVerified     bool     `json:"email_verified"`
	PreferredUsername string   `json:"preferred_username"`
	Name              string   `json:"name"`
	Groups            []string `json:"groups"`
	Roles             []string `json:"roles"`
}

// NewOIDCVerifier discovers the issuer and builds a verifier for the SPA audience.
func NewOIDCVerifier(ctx context.Context, cfg *config.Config) (*OIDCVerifier, error) {
	if cfg.OIDCIssuerURL == "" || cfg.OIDCClientID == "" {
		return nil, errors.New("OIDC verifier requires OIDC_ISSUER_URL and OIDC_CLIENT_ID")
	}
	provider, err := oidc.NewProvider(ctx, cfg.OIDCIssuerURL)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery: %w", err)
	}
	audience := cfg.OIDCAudience
	if audience == "" {
		audience = cfg.OIDCClientID
	}
	return &OIDCVerifier{
		verifier: provider.Verifier(&oidc.Config{ClientID: audience}),
		issuer:   cfg.OIDCIssuerURL,
	}, nil
}

// Verify validates a bearer ID token and returns claims.
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
	if c.Subject == "" {
		return nil, errors.New("token missing sub claim")
	}
	c.Issuer = v.issuer
	c.Groups = mergeUnique(c.Groups, c.Roles)
	return &c, nil
}

func mergeUnique(a, b []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(a)+len(b))
	for _, v := range append(append([]string{}, a...), b...) {
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
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
