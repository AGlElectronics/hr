// Package audit records every HR state-changing write to an append-only log.
// The log is intended to be observable by HR admins and downstream SOCs;
// it never stores secrets.
package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/AGlElectronics/hr/backend/internal/actor"
)

// SystemIssuer is the actor issuer of background jobs ("system:role-gate").
const SystemIssuer = "system"

// Writer appends events to the audit_events table.
type Writer struct {
	pool   *pgxpool.Pool
	logger zerolog.Logger
}

// NewWriter returns an audit Writer bound to pool.
func NewWriter(pool *pgxpool.Pool, logger zerolog.Logger) *Writer {
	return &Writer{pool: pool, logger: logger.With().Str("component", "audit").Logger()}
}

// identity returns the issuer and subject of the actor: the signed-in caller
// from ctx, or "system" plus the job name for background work.
func identity(ctx context.Context, name string) (issuer, subject string) {
	if a, ok := actor.FromContext(ctx); ok && a.Username == name {
		return a.Issuer, a.Subject
	}
	if job, ok := strings.CutPrefix(name, "system:"); ok {
		return SystemIssuer, job
	}
	return "", ""
}

// Record inserts a single audit event. Metadata, if non-nil, is marshalled
// to JSON — pass structs or map[string]any. The actor's issuer and subject
// come from ctx (or "system" for "system:<job>" actors). If the DB write
// fails the error is logged and returned so callers can decide whether to
// fail the parent request; most call sites should surface the error.
func (w *Writer) Record(ctx context.Context, actorName, action, entityType string, entityID uuid.UUID, metadata any) error {
	if w == nil || w.pool == nil {
		return fmt.Errorf("audit writer not initialised")
	}

	var metaBytes []byte
	if metadata != nil {
		b, err := json.Marshal(metadata)
		if err != nil {
			return fmt.Errorf("marshal audit metadata: %w", err)
		}
		metaBytes = b
	}
	issuer, subject := identity(ctx, actorName)

	const q = `
INSERT INTO audit_events (actor_name, actor_issuer, actor_subject, action, entity_type, entity_id, metadata)
VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb)
`
	_, err := w.pool.Exec(ctx, q, actorName, issuer, subject, action, entityType, entityID, metaBytes)
	if err != nil {
		w.logger.Error().Err(err).
			Str("actor", actorName).
			Str("action", action).
			Str("entity_type", entityType).
			Str("entity_id", entityID.String()).
			Msg("audit insert failed")
		return fmt.Errorf("audit insert: %w", err)
	}

	w.logger.Info().
		Str("actor", actorName).
		Str("actor_subject", subject).
		Str("action", action).
		Str("entity_type", entityType).
		Str("entity_id", entityID.String()).
		Msg("audit event")
	return nil
}
