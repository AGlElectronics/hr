package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/AGlElectronics/hr/backend/internal/actor"
	"github.com/AGlElectronics/hr/backend/internal/httperror"
)

// Job kinds.
const (
	JobOnboardingComplete = "onboarding.complete"
	JobBundleSync         = "bundle.sync"
)

// Job is one row of background_jobs.
type Job struct {
	ID          uuid.UUID       `json:"id"`
	Kind        string          `json:"kind"`
	Payload     json.RawMessage `json:"payload"`
	Status      string          `json:"status"`
	Attempts    int             `json:"attempts"`
	MaxAttempts int             `json:"max_attempts"`
	LastError   string          `json:"last_error,omitempty"`
	CreatedBy   string          `json:"created_by"`
	Result      json.RawMessage `json:"result,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	FinishedAt  *time.Time      `json:"finished_at,omitempty"`

	createdByIssuer, createdBySubject string
}

// JobHandler runs one job. A returned error retries the job until its
// attempts run out.
type JobHandler struct {
	Run func(ctx context.Context, job Job) error
	// GiveUp runs once when the job failed for the last time (or its lease
	// ran out on the last attempt), e.g. to put a draft back to "draft".
	GiveUp func(ctx context.Context, job Job, reason string)
}

// JobQueue is HR's database-backed job queue. A worker claims due jobs with
// a lease (locked_until); a job whose worker died is claimed again once the
// lease runs out, counting as a new attempt.
type JobQueue struct {
	pool     *pgxpool.Pool
	logger   zerolog.Logger
	lease    time.Duration
	handlers map[string]JobHandler
	wake     chan struct{}
	// inline runs due jobs right after Enqueue in a goroutine (tests and
	// routers without a worker loop).
	inline bool
	mu     sync.Mutex
}

// NewJobQueue wires a JobQueue. inline runs jobs right away instead of
// waiting for Run.
func NewJobQueue(pool *pgxpool.Pool, logger zerolog.Logger, inline bool) *JobQueue {
	return &JobQueue{pool: pool, logger: logger.With().Str("component", "jobs").Logger(), lease: 5 * time.Minute,
		handlers: map[string]JobHandler{}, wake: make(chan struct{}, 1), inline: inline}
}

// Handle registers the handler of a job kind.
func (q *JobQueue) Handle(kind string, h JobHandler) { q.handlers[kind] = h }

// Enqueue adds a job and wakes the worker.
func (q *JobQueue) Enqueue(ctx context.Context, kind string, payload any, createdBy string, maxAttempts int) (uuid.UUID, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return uuid.Nil, err
	}
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	var issuer, subject string
	if a, ok := actor.FromContext(ctx); ok && a.Username == createdBy {
		issuer, subject = a.Issuer, a.Subject
	}
	var id uuid.UUID
	if err := q.pool.QueryRow(ctx, `
INSERT INTO background_jobs (kind, payload, created_by, created_by_issuer, created_by_subject, max_attempts)
VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		kind, raw, createdBy, issuer, subject, maxAttempts).Scan(&id); err != nil {
		return uuid.Nil, httperror.Internal(err)
	}
	q.Kick()
	return id, nil
}

// Kick makes the worker look for due jobs now.
func (q *JobQueue) Kick() {
	if q.inline {
		go func() { _ = q.RunDue(context.Background()) }()
		return
	}
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

const jobCols = `id, kind, payload, status, attempts, max_attempts, last_error, created_by, created_by_issuer, created_by_subject,
COALESCE(result, 'null'::jsonb), created_at, finished_at`

func scanJob(row rowScanner) (*Job, error) {
	j := &Job{}
	err := row.Scan(&j.ID, &j.Kind, &j.Payload, &j.Status, &j.Attempts, &j.MaxAttempts, &j.LastError, &j.CreatedBy,
		&j.createdByIssuer, &j.createdBySubject, &j.Result, &j.CreatedAt, &j.FinishedAt)
	return j, err
}

// SetResult stores what a job produced (shown to whoever waits for it).
func (q *JobQueue) SetResult(ctx context.Context, id uuid.UUID, result any) error {
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	_, err = q.pool.Exec(ctx, `UPDATE background_jobs SET result = $2 WHERE id = $1`, id, raw)
	return err
}

// Get returns one job.
func (q *JobQueue) Get(ctx context.Context, id uuid.UUID) (*Job, error) {
	j, err := scanJob(q.pool.QueryRow(ctx, `SELECT `+jobCols+` FROM background_jobs WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, httperror.NotFound("job")
	}
	if err != nil {
		return nil, httperror.Internal(err)
	}
	return j, nil
}

// Run processes jobs until ctx ends: every interval, and whenever Kick is
// called.
func (q *JobQueue) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 10 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if err := q.RunDue(ctx); err != nil && ctx.Err() == nil {
			q.logger.Warn().Err(err).Msg("jobs: cycle failed")
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-q.wake:
		}
	}
}

// RunDue runs every due job once (also used by tests).
func (q *JobQueue) RunDue(ctx context.Context) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if err := q.giveUpExpired(ctx); err != nil {
		return err
	}
	for {
		job, err := q.claim(ctx)
		if err != nil || job == nil {
			return err
		}
		q.run(ctx, *job)
	}
}

// giveUpExpired fails jobs whose last attempt's lease ran out.
func (q *JobQueue) giveUpExpired(ctx context.Context) error {
	rows, err := q.pool.Query(ctx, `
UPDATE background_jobs SET status = 'failed', finished_at = now(), locked_until = NULL,
       last_error = 'interrupted: the worker stopped during the last attempt'
 WHERE status = 'running' AND locked_until < now() AND attempts >= max_attempts
RETURNING `+jobCols)
	if err != nil {
		return err
	}
	var jobs []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			rows.Close()
			return err
		}
		jobs = append(jobs, *j)
	}
	rows.Close()
	for _, j := range jobs {
		if h, ok := q.handlers[j.Kind]; ok && h.GiveUp != nil {
			h.GiveUp(ctx, j, j.LastError)
		}
	}
	return rows.Err()
}

func (q *JobQueue) claim(ctx context.Context) (*Job, error) {
	j, err := scanJob(q.pool.QueryRow(ctx, `
UPDATE background_jobs SET status = 'running', attempts = attempts + 1, locked_until = now() + make_interval(secs => $1)
 WHERE id = (SELECT id FROM background_jobs
              WHERE (status = 'pending' AND run_after <= now())
                 OR (status = 'running' AND locked_until < now() AND attempts < max_attempts)
              ORDER BY run_after FOR UPDATE SKIP LOCKED LIMIT 1)
RETURNING `+jobCols, q.lease.Seconds()))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return j, err
}

func (q *JobQueue) run(ctx context.Context, j Job) {
	log := q.logger.With().Str("job_id", j.ID.String()).Str("kind", j.Kind).Int("attempt", j.Attempts).Logger()
	h, ok := q.handlers[j.Kind]
	var err error
	if !ok {
		err = fmt.Errorf("no handler for job kind %q", j.Kind)
	} else {
		// Jobs act as the person who queued them in the audit log.
		jctx := actor.With(ctx, &actor.Actor{Username: j.CreatedBy, Issuer: j.createdByIssuer, Subject: j.createdBySubject})
		func() {
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("panic: %v", r)
				}
			}()
			err = h.Run(jctx, j)
		}()
	}
	if err == nil {
		_, _ = q.pool.Exec(ctx, `UPDATE background_jobs SET status = 'succeeded', finished_at = now(), locked_until = NULL, last_error = '' WHERE id = $1`, j.ID)
		log.Info().Msg("job succeeded")
		return
	}
	msg := strings.TrimPrefix(err.Error(), ErrPermanent.Error()+": ")
	if j.Attempts >= j.MaxAttempts || errors.Is(err, ErrPermanent) {
		// Clean up before the job shows as failed, so whoever waits for it
		// sees the final state.
		if ok && h.GiveUp != nil {
			h.GiveUp(ctx, j, msg)
		}
		_, _ = q.pool.Exec(ctx, `UPDATE background_jobs SET status = 'failed', finished_at = now(), locked_until = NULL, last_error = $2 WHERE id = $1`, j.ID, msg)
		log.Warn().Err(err).Msg("job failed")
		return
	}
	backoff := time.Duration(j.Attempts*j.Attempts) * 30 * time.Second
	_, _ = q.pool.Exec(ctx, `
UPDATE background_jobs SET status = 'pending', locked_until = NULL, last_error = $2, run_after = now() + make_interval(secs => $3)
 WHERE id = $1`, j.ID, msg, backoff.Seconds())
	log.Warn().Err(err).Dur("retry_in", backoff).Msg("job failed; will retry")
}

// ErrPermanent marks a job error that retrying cannot fix.
var ErrPermanent = errors.New("permanent")

// Permanent wraps err so the queue does not retry it.
func Permanent(err error) error { return fmt.Errorf("%w: %w", ErrPermanent, err) }
