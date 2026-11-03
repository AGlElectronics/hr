-- sqlc query definitions for approvals.

-- name: ListApprovalsForDraft :many
SELECT id, draft_id, decision, reason, approved_by, approved_at
FROM approvals
WHERE draft_id = $1
ORDER BY approved_at DESC;

-- name: CreateApproval :one
INSERT INTO approvals (draft_id, decision, reason, approved_by)
VALUES ($1, $2, $3, $4)
RETURNING id, draft_id, decision, reason, approved_by, approved_at;
