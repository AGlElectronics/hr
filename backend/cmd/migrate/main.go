// Package main is the HR database migrator. It is a one-shot container that
// applies all pending Goose migrations against DATABASE_URL and exits. The
// API server (cmd/server) MUST NOT run migrations on boot — Compose and
// Kubernetes orchestrate ordering by waiting for this to exit zero.
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/joho/godotenv"
	"github.com/pressly/goose/v3"
	"github.com/rs/zerolog"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/AGlElectronics/hr/backend/migrations"
)

func main() {
	logger := zerolog.New(os.Stdout).With().Timestamp().Logger()
	zerolog.TimeFieldFormat = time.RFC3339
	if os.Getenv("APP_ENV") == "development" {
		logger = logger.Output(zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339})
	}

	_ = godotenv.Load()

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		logger.Fatal().Msg("DATABASE_URL is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if err := run(ctx, logger, dsn); err != nil {
		logger.Fatal().Err(err).Msg("migrate failed")
	}
	logger.Info().Msg("migrations applied; exiting")
}

func run(ctx context.Context, logger zerolog.Logger, dsn string) error {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("open sql.DB: %w", err)
	}
	defer func() { _ = db.Close() }()

	if err := waitForDB(ctx, logger, db); err != nil {
		return err
	}

	goose.SetBaseFS(migrations.FS)
	goose.SetTableName("goose_db_version")
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("set goose dialect: %w", err)
	}
	if err := goose.UpContext(ctx, db, "."); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}

// waitForDB polls up to ~30s because the Compose healthcheck is best-effort
// and the migrate container can start before Postgres finishes accepting
// connections on cold boots.
func waitForDB(ctx context.Context, logger zerolog.Logger, db *sql.DB) error {
	deadline := time.Now().Add(30 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		err := db.PingContext(pingCtx)
		cancel()
		if err == nil {
			return nil
		}
		lastErr = err
		logger.Warn().Err(err).Msg("waiting for database")
		time.Sleep(2 * time.Second)
	}
	if lastErr == nil {
		lastErr = errors.New("database never became ready")
	}
	return fmt.Errorf("ping database: %w", lastErr)
}
