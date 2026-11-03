-- sqlc query definitions for employment_drafts.

-- name: GetDraft :one
SELECT id, person_id, title, start_date, end_date, state, notes, created_by, created_at, updated_at
FROM employment_drafts
WHERE id = $1;

-- name: ListDrafts :many
SELECT id, person_id, title, start_date, end_date, state, notes, created_by, created_at, updated_at
FROM employment_drafts
ORDER BY updated_at DESC;

-- name: ListDraftsByState :many
SELECT id, person_id, title, start_date, end_date, state, notes, created_by, created_at, updated_at
FROM employment_drafts
WHERE state = $1
ORDER BY updated_at DESC;

-- name: CreateDraft :one
INSERT INTO employment_drafts (person_id, title, start_date, end_date, notes, created_by)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id, person_id, title, start_date, end_date, state, notes, created_by, created_at, updated_at;

-- name: UpdateDraftState :one
UPDATE employment_drafts
   SET state = $2, updated_at = now()
 WHERE id = $1
 RETURNING id, person_id, title, start_date, end_date, state, notes, created_by, created_at, updated_at;
