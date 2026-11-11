package service

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AGlElectronics/hr/backend/internal/httperror"
)

// AccessHistoryActions are the audit actions kept after an offboarding (for
// Retention.OffboardedAuditMonths): who granted or removed which access,
// when. Everything else about the person is deleted with them.
var AccessHistoryActions = []string{
	"onboarding.complete", "person.access.change", "person.status.paused", "person.status.active",
	"person.ban", "person.unban", "person.offboard", "role.grant_overridden", "role.granted_by_training",
	"role.revoked_expired", "bundle.sync", "department.leads", "review.accept", "review.merge", "review.reject",
	"workspace.suspend", "workspace.delete",
}

// AuditEvent is one entry of the audit log as the API returns it.
type AuditEvent struct {
	ID           uuid.UUID       `json:"id"`
	ActorName    string          `json:"actor_name"`
	ActorIssuer  string          `json:"actor_issuer"`
	ActorSubject string          `json:"actor_subject"`
	Action       string          `json:"action"`
	EntityType   string          `json:"entity_type"`
	EntityID     uuid.UUID       `json:"entity_id"`
	Metadata     json.RawMessage `json:"metadata,omitempty"`
	OccurredAt   time.Time       `json:"occurred_at"`
	PurgeAfter   *time.Time      `json:"purge_after,omitempty"`
}

// AuditFilter narrows the audit log.
type AuditFilter struct {
	EntityType string
	EntityID   uuid.UUID
	// Actor matches the actor's name or subject.
	Actor string
	// Action matches the action or an action prefix ("person." ).
	Action        string
	Since, Before *time.Time
	Limit, Offset int
}

// AuditService reads the audit log and purges expired history.
type AuditService struct{ pool *pgxpool.Pool }

// NewAuditService wires an AuditService.
func NewAuditService(pool *pgxpool.Pool) *AuditService { return &AuditService{pool: pool} }

// List returns one page of audit events, newest first.
func (s *AuditService) List(ctx context.Context, f AuditFilter) (*Page[AuditEvent], error) {
	f.Limit, f.Offset = NormalizePaging(f.Limit, f.Offset)
	action := strings.TrimSpace(f.Action)
	rows, err := s.pool.Query(ctx, `
SELECT id, actor_name, actor_issuer, actor_subject, action, entity_type, entity_id, COALESCE(metadata, 'null'::jsonb),
       occurred_at, purge_after, count(*) OVER ()
  FROM audit_events
 WHERE ($1 = '' OR entity_type = $1)
   AND ($2 = '00000000-0000-0000-0000-000000000000'::uuid OR entity_id = $2)
   AND ($3 = '' OR actor_name = $3 OR actor_subject = $3)
   AND ($4 = '' OR action = $4 OR (right($4, 1) = '.' AND action LIKE $4 || '%'))
   AND ($5::timestamptz IS NULL OR occurred_at >= $5)
   AND ($6::timestamptz IS NULL OR occurred_at < $6)
 ORDER BY occurred_at DESC, id
 LIMIT $7 OFFSET $8`, strings.TrimSpace(f.EntityType), f.EntityID, strings.TrimSpace(f.Actor), action, f.Since, f.Before, f.Limit, f.Offset)
	if err != nil {
		return nil, httperror.Internal(err)
	}
	defer rows.Close()
	out := &Page[AuditEvent]{Items: []AuditEvent{}, Limit: f.Limit, Offset: f.Offset}
	for rows.Next() {
		var e AuditEvent
		var meta []byte
		if err := rows.Scan(&e.ID, &e.ActorName, &e.ActorIssuer, &e.ActorSubject, &e.Action, &e.EntityType, &e.EntityID, &meta,
			&e.OccurredAt, &e.PurgeAfter, &out.Total); err != nil {
			return nil, httperror.Internal(err)
		}
		if string(meta) != "null" {
			e.Metadata = meta
		}
		out.Items = append(out.Items, e)
	}
	return out, rows.Err()
}

// PurgeExpired deletes kept access history whose period has ended.
func (s *AuditService) PurgeExpired(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM audit_events WHERE purge_after IS NOT NULL AND purge_after < now()`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
