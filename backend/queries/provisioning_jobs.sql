-- sqlc query definitions for provisioning_jobs.

-- name: ListJobsForDraft :many
SELECT id, draft_id, target, status, attempt, message, external_id, started_at, finished_at
FROM provisioning_jobs
WHERE draft_id = $1
ORDER BY started_at DESC;

-- name: CreateJob :one
INSERT INTO provisioning_jobs (draft_id, target, status, attempt)
VALUES ($1, $2, $3, $4)
RETURNING id, draft_id, target, status, attempt, message, external_id, started_at, finished_at;

-- name: FinishJob :one
UPDATE provisioning_jobs
   SET status      = $2,
       message     = $3,
       external_id = $4,
       finished_at = now()
 WHERE id = $1
 RETURNING id, draft_id, target, status, attempt, message, external_id, started_at, finished_at;
