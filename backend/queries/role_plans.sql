-- sqlc query definitions for role_plans.

-- name: ListRolePlansForDraft :many
SELECT id, draft_id, role_name, group_name, scope, created_at
FROM role_plans
WHERE draft_id = $1
ORDER BY role_name;

-- name: CreateRolePlan :one
INSERT INTO role_plans (draft_id, role_name, group_name, scope)
VALUES ($1, $2, $3, $4)
RETURNING id, draft_id, role_name, group_name, scope, created_at;

-- name: DeleteRolePlansForDraft :exec
DELETE FROM role_plans WHERE draft_id = $1;
