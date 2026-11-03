-- sqlc query definitions for audit_events.

-- name: InsertAuditEvent :one
INSERT INTO audit_events (actor_name, action, entity_type, entity_id, metadata)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, actor_name, action, entity_type, entity_id, metadata, occurred_at;

-- name: ListAuditEventsForEntity :many
SELECT id, actor_name, action, entity_type, entity_id, metadata, occurred_at
FROM audit_events
WHERE entity_type = $1 AND entity_id = $2
ORDER BY occurred_at DESC
LIMIT $3 OFFSET $4;
