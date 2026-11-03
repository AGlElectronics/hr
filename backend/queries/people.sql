-- sqlc query definitions for the `people` table.
-- These are not yet consumed by services (see sqlc.yaml), but they
-- describe the intended repository surface so sqlc generation will
-- produce useful bindings when we flip the services over.

-- name: GetPerson :one
SELECT id, legal_name, display_name, email, created_at, updated_at
FROM people
WHERE id = $1;

-- name: ListPeople :many
SELECT id, legal_name, display_name, email, created_at, updated_at
FROM people
ORDER BY lower(display_name);

-- name: CreatePerson :one
INSERT INTO people (legal_name, display_name, email)
VALUES ($1, $2, $3)
RETURNING id, legal_name, display_name, email, created_at, updated_at;

-- name: UpdatePerson :one
UPDATE people
   SET legal_name   = COALESCE(sqlc.narg(legal_name), legal_name),
       display_name = COALESCE(sqlc.narg(display_name), display_name),
       email        = COALESCE(sqlc.narg(email), email),
       updated_at   = now()
 WHERE id = $1
 RETURNING id, legal_name, display_name, email, created_at, updated_at;
