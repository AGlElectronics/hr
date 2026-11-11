// Package app wires configuration, the database pool and HTTP into a runnable
// server. main.go calls Run.
//
// IMPORTANT: the server does NOT run migrations on boot. cmd/migrate is a
// separate one-shot container (and binary). This keeps API startup fast,
// avoids racey leader-election, and makes rollbacks predictable.
package app

import (
	"context"
	"errors"
	"fmt"
	nethttp "net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/AGlElectronics/hr/backend/internal/auth"
	"github.com/AGlElectronics/hr/backend/internal/config"
	"github.com/AGlElectronics/hr/backend/internal/db"
	"github.com/AGlElectronics/hr/backend/internal/denlink"
	httpx "github.com/AGlElectronics/hr/backend/internal/http"
	"github.com/AGlElectronics/hr/backend/internal/mail"
	"github.com/AGlElectronics/hr/backend/internal/provisioners"
	"github.com/AGlElectronics/hr/backend/internal/service"
	"github.com/AGlElectronics/hr/backend/internal/tlscert"
)

// Version is set via -ldflags at build time.
var Version = "dev"

// Run boots the server. It blocks until a shutdown signal is received.
func Run(ctx context.Context, logger zerolog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	logger.Info().
		Str("env", cfg.AppEnv).
		Str("addr", cfg.HTTPAddr).
		Str("version", Version).
		Str("summary", cfg.String()).
		Msg("starting hr backend")

	dbCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	pool, err := db.Open(dbCtx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer pool.Close()
	logger.Info().Msg("database pool ready")

	var idp provisioners.IdPProvisioner
	switch {
	case cfg.ProvisionerMode == config.ProvisionerOff:
		logger.Info().Msg("IDP_PROVISIONER_MODE=off: HR keeps records only and touches no accounts")
	case cfg.KeycloakConfigured():
		kc, err := provisioners.NewKeycloakIdP(provisioners.KeycloakConfig{
			BaseURL:          cfg.KeycloakBaseURL,
			Realm:            cfg.KeycloakRealm,
			ClientID:         cfg.KeycloakClientID,
			ClientSecret:     cfg.KeycloakClientSecret,
			SetupClientID:    cfg.KeycloakSetupClientID,
			SetupRedirectURI: cfg.KeycloakSetupRedirectURI,
			LinkOnly:         cfg.ProvisionerMode == config.ProvisionerLink,
			GroupParent:      cfg.KeycloakGroupParent,
			GroupPrefix:      cfg.KeycloakGroupPrefix,
			Attributes:       cfg.KeycloakUserAttributes,
		}, logger)
		if err != nil {
			return fmt.Errorf("keycloak idp: %w", err)
		}
		idp = kc
		logger.Info().
			Str("base_url", cfg.KeycloakBaseURL).
			Str("realm", cfg.KeycloakRealm).
			Msg("keycloak idp provisioner ready")
	default:
		// config.Load refuses this outside APP_ENV=development.
		idp = provisioners.NewFakeIdP(logger)
		logger.Warn().Msg("KEYCLOAK_* not set: development only, using an in-memory fake identity provider (no real accounts)")
	}

	var oidcVerifier *auth.OIDCVerifier
	if cfg.AuthMode == config.AuthModeOIDC {
		v, err := auth.NewOIDCVerifier(ctx, cfg)
		if err != nil {
			return fmt.Errorf("oidc verifier: %w", err)
		}
		oidcVerifier = v
		logger.Info().Str("issuer", cfg.OIDCIssuerURL).Msg("oidc verifier ready")
	}

	if cfg.S3Configured() {
		logger.Info().
			Str("endpoint", cfg.S3Endpoint).
			Str("bucket", cfg.S3Bucket).
			Msg("object storage configured (LOCAL DEV ONLY; MinIO must never be used in production)")
	}

	var mailer mail.Sender
	if m := mail.NewSMTP(mail.SMTPConfig{
		Host: cfg.SMTPHost, Port: cfg.SMTPPort, Username: cfg.SMTPUsername, Password: cfg.SMTPPassword,
		From: cfg.SMTPFrom, FromName: cfg.SMTPFromName,
	}); m != nil {
		mailer = m
		logger.Info().Str("host", cfg.SMTPHost).Msg("smtp mailer ready")
	} else {
		logger.Warn().Msg("SMTP_HOST not set — HR cannot email offboarding notices")
	}

	den := denlink.New(pool, denlink.Config{Slug: "hr", Version: Version, InternalURL: cfg.DenInternalURL, Mode: cfg.DenMode}, logger)
	if err := den.Restore(ctx); err != nil {
		logger.Warn().Err(err).Msg("den link: load failed")
	}
	go den.Run(ctx)

	gate := httpx.NewRoleGate(cfg, pool, idp, logger)
	if gate.Active() {
		logger.Info().Str("holdings_url", cfg.LearnHoldingsURL).Dur("interval", cfg.RoleGateInterval).Msg("training requirements enforced")
		go gate.Run(ctx, cfg.RoleGateInterval)
	} else {
		logger.Warn().Msg("LEARN_HOLDINGS_URL/LEARN_SERVICE_TOKEN not set — training requirements are not enforced")
	}

	jobs := service.NewJobQueue(pool, logger, false)
	services := &httpx.Services{}
	router := httpx.NewRouter(httpx.Deps{
		RoleGate:     gate,
		Config:       cfg,
		Pool:         pool,
		Logger:       logger,
		OIDCVerifier: oidcVerifier,
		IdP:          idp,
		Mailer:       mailer,
		Den:          den,
		Jobs:         jobs,
		Services:     services,
	}, Version)
	go jobs.Run(ctx, cfg.JobInterval)
	go services.Maintenance.Run(ctx, cfg.MaintenanceInterval)
	if cfg.ReviewScanInterval > 0 {
		go services.Review.RunScans(ctx, cfg.ReviewScanInterval)
	}

	server := &nethttp.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	if _, err := tlscert.Configure(ctx, server, logger); err != nil {
		return fmt.Errorf("tls: %w", err)
	}

	return runServer(ctx, server, pool, cfg.ShutdownTimeout, logger)
}

func runServer(ctx context.Context, server *nethttp.Server, pool *pgxpool.Pool, shutdownTimeout time.Duration, logger zerolog.Logger) error {
	signalCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		logger.Info().Str("addr", server.Addr).Msg("http listening")
		if err := tlscert.ListenAndServe(server); err != nil && !errors.Is(err, nethttp.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-signalCtx.Done():
		logger.Info().Msg("shutdown signal received")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error().Err(err).Msg("http shutdown error")
	}
	pool.Close()
	logger.Info().Msg("shutdown complete")
	return nil
}
