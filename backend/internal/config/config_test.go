package config

import (
	"strings"
	"testing"
)

func validProd() *Config {
	return &Config{
		AppEnv:               "production",
		DatabaseURL:          "postgres://x",
		AuthMode:             AuthModeOIDC,
		OIDCIssuerURL:        "https://auth.example.org/realms/conservices",
		OIDCClientID:         "hr-spa",
		ProvisionerMode:      ProvisionerManage,
		KeycloakBaseURL:      "https://keycloak",
		KeycloakClientSecret: "a-real-secret",
	}
}

func TestValidateProduction(t *testing.T) {
	if err := validProd().validate(nil); err != nil {
		t.Fatalf("valid production config refused: %v", err)
	}
}

func TestValidateRefusesDevSecretsOutsideDevelopment(t *testing.T) {
	c := validProd()
	c.KeycloakClientSecret = "hr-provisioner-dev-secret"
	c.OffboardHooks = "http://wiki/api/internal/offboarded|wiki-service-dev-token"
	err := c.validate(nil)
	if err == nil || !strings.Contains(err.Error(), "KEYCLOAK_CLIENT_SECRET") || !strings.Contains(err.Error(), "OFFBOARD_HOOKS") {
		t.Fatalf("want dev secrets refused, got %v", err)
	}

	c.AppEnv = "development"
	if err := c.validate(nil); err != nil {
		t.Fatalf("development may use dev secrets: %v", err)
	}
}

func TestValidateDirectoryToken(t *testing.T) {
	c := validProd()
	c.DirectoryToken = "short"
	if err := c.validate(nil); err == nil || !strings.Contains(err.Error(), "HR_DIRECTORY_TOKEN must be at least 16") {
		t.Fatalf("want short token refused, got %v", err)
	}
	c.DirectoryToken = "hr-directory-dev-token"
	if err := c.validate(nil); err == nil || !strings.Contains(err.Error(), "HR_DIRECTORY_TOKEN uses the development value") {
		t.Fatalf("want dev token refused, got %v", err)
	}
	c.DirectoryToken = "a-long-random-directory-token"
	if err := c.validate(nil); err != nil {
		t.Fatalf("real token refused: %v", err)
	}
}

func TestValidateProvisionerMode(t *testing.T) {
	c := validProd()
	c.KeycloakBaseURL, c.KeycloakClientSecret = "", ""
	if err := c.validate(nil); err == nil || !strings.Contains(err.Error(), "IDP_PROVISIONER_MODE=manage needs") {
		t.Fatalf("manage without Keycloak in production: want error, got %v", err)
	}
	c.ProvisionerMode = ProvisionerOff
	if err := c.validate(nil); err != nil {
		t.Fatalf("off mode needs no Keycloak: %v", err)
	}
	c.ProvisionerMode = "sometimes"
	if err := c.validate(nil); err == nil {
		t.Fatal("unknown mode accepted")
	}
	c.AppEnv, c.ProvisionerMode = "development", ProvisionerManage
	if err := c.validate(nil); err != nil {
		t.Fatalf("development may run without Keycloak (fake IdP): %v", err)
	}
}

func TestMaskDSN(t *testing.T) {
	if got := maskDSN("postgres://hr:secret@db:5432/hr"); got != "postgres://***:***@db:5432/hr" {
		t.Fatalf("maskDSN = %q", got)
	}
}
