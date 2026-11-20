// Package main is the HR API server entrypoint. Migrations are a separate
// one-shot (cmd/migrate); the API MUST NOT auto-migrate.
package main

import (
	"context"
	"os"
	"time"

	"github.com/rs/zerolog"

	"github.com/AGlElectronics/hr/backend/internal/app"
	"github.com/AGlElectronics/hr/backend/internal/probe"
)

func main() {
	// `server healthcheck` probes the running server's /livez
	// (Docker HEALTHCHECK / Compose; the image has no curl).
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		addr := os.Getenv("HTTP_ADDR")
		if addr == "" {
			addr = ":8080"
		}
		probe.Run(addr)
	}
	logger := zerolog.New(os.Stdout).With().Timestamp().Logger()
	zerolog.TimeFieldFormat = time.RFC3339

	if os.Getenv("APP_ENV") == "development" {
		logger = logger.Output(zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339})
	}

	ctx := context.Background()
	if err := app.Run(ctx, logger); err != nil {
		logger.Fatal().Err(err).Msg("server failed")
	}
}
