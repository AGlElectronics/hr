package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AGlElectronics/hr/backend/internal/audit"
	"github.com/AGlElectronics/hr/backend/internal/domain"
	"github.com/AGlElectronics/hr/backend/internal/httperror"
	"github.com/AGlElectronics/hr/backend/internal/provisioners"
)

// ProvisionService completes onboardings: it queues a background job that
// creates or links the IdP account, applies the groups, has the IdP email
// the set-password link, and marks the onboarding and the person active.
type ProvisionService struct {
	pool     *pgxpool.Pool
	drafts   *DraftService
	people   *PeopleService
	idp      provisioners.IdPProvisioner // nil when IDP_PROVISIONER_MODE=off
	audit    *audit.Writer
	settings *SettingsService
	gate     *RoleGateService // nil = no training requirements
	jobs     *JobQueue
	depts    *DepartmentService
	// sendSetup emails new accounts a set-password link
	// (KEYCLOAK_SEND_PASSWORD_SETUP).
	sendSetup bool
	// workspace creates the Google Workspace account (nil: off).
	workspace *WorkspaceService
}

// NewProvisionService wires a ProvisionService. idp is nil in off mode.
func NewProvisionService(pool *pgxpool.Pool, drafts *DraftService, people *PeopleService, idp provisioners.IdPProvisioner, aud *audit.Writer, settings *SettingsService) *ProvisionService {
	return &ProvisionService{pool: pool, drafts: drafts, people: people, idp: idp, audit: aud, settings: settings, sendSetup: true}
}

// SetRoleGate holds roles with unmet training requirements back at Complete.
func (s *ProvisionService) SetRoleGate(g *RoleGateService) { s.gate = g }

// SetPasswordSetup turns the set-password email for new accounts on or off.
func (s *ProvisionService) SetPasswordSetup(send bool) { s.sendSetup = send }

// SetDepartments gives leads their lead groups when their account is made.
func (s *ProvisionService) SetDepartments(d *DepartmentService) { s.depts = d }

// SetWorkspace creates Google Workspace accounts after the IdP account.
func (s *ProvisionService) SetWorkspace(w *WorkspaceService) { s.workspace = w }

// Register runs onboarding completions on the job queue.
func (s *ProvisionService) Register(q *JobQueue) {
	s.jobs = q
	q.Handle(JobOnboardingComplete, JobHandler{Run: s.runJob, GiveUp: s.giveUp})
}

// CompleteResult is what the onboarding overview shows after "Complete"
// (Queued) and, once the job ran, in the onboarding's provisioning status.
type CompleteResult struct {
	OK                     bool                     `json:"ok"`
	Queued                 bool                     `json:"queued,omitempty"`
	JobID                  *uuid.UUID               `json:"job_id,omitempty"`
	Error                  string                   `json:"error,omitempty"`
	Draft                  *domain.EmploymentDraft  `json:"draft,omitempty"`
	Jobs                   []domain.ProvisioningJob `json:"jobs"`
	GroupsApplied          []string                 `json:"groups_applied"`
	AccountCreated         bool                     `json:"account_created"`
	PasswordSetupEmailSent bool                     `json:"password_setup_email_sent"`
	PasswordSetupError     string                   `json:"password_setup_error,omitempty"`
	// PasswordSetupSkipped: no email by configuration (brokered accounts),
	// or the account already existed.
	PasswordSetupSkipped bool `json:"password_setup_skipped,omitempty"`
	// PendingGrants are roles left out until the person finishes training.
	PendingGrants []PendingGrant `json:"pending_grants"`
	// Warnings are things HR could not do but that do not fail the
	// onboarding (Google Workspace).
	Warnings []string `json:"warnings,omitempty"`
}

// ProvisioningStatus is the state of an onboarding's latest completion job.
type ProvisioningStatus struct {
	JobID       uuid.UUID       `json:"job_id"`
	Status      string          `json:"status"` // pending | running | succeeded | failed
	Attempts    int             `json:"attempts"`
	MaxAttempts int             `json:"max_attempts"`
	Error       string          `json:"error,omitempty"`
	Result      *CompleteResult `json:"result,omitempty"`
	QueuedAt    time.Time       `json:"queued_at"`
}

// Complete checks an onboarding HR reviewed and queues its completion. The
// onboarding stays "provisioning" until the job is done; on failure it goes
// back to "draft" so HR can fix things and retry.
func (s *ProvisionService) Complete(ctx context.Context, actor string, draftID uuid.UUID) (*CompleteResult, error) {
	draft, err := s.drafts.Get(ctx, draftID)
	if err != nil {
		return nil, err
	}
	if draft.State != domain.DraftStateDraft {
		return nil, httperror.Conflict(fmt.Sprintf("onboarding is already %q", draft.State))
	}
	if draft.DepartmentID == nil {
		return nil, httperror.Validation("pick a department before completing", map[string]string{"department_id": "required"})
	}
	person, err := s.people.Get(ctx, draft.PersonID)
	if err != nil {
		return nil, err
	}
	if missing := MissingFields(person); len(missing) > 0 {
		return nil, httperror.Validation("volunteer profile is incomplete", missing)
	}
	if person.IsBanned {
		return nil, httperror.Conflict("this person is banned and cannot be onboarded")
	}
	if s.jobs == nil {
		return nil, httperror.Internal(errors.New("provisioning job queue not wired"))
	}
	updated, err := s.drafts.Transition(ctx, draft.ID, domain.DraftStateProvisioning)
	if err != nil {
		return nil, err
	}
	jobID, err := s.jobs.Enqueue(ctx, JobOnboardingComplete, map[string]string{"draft_id": draftID.String()}, actor, 3)
	if err != nil {
		_, _ = s.drafts.Transition(ctx, draft.ID, domain.DraftStateDraft)
		return nil, err
	}
	return &CompleteResult{Queued: true, JobID: &jobID, Draft: updated, Jobs: []domain.ProvisioningJob{},
		GroupsApplied: []string{}, PendingGrants: []PendingGrant{}}, nil
}

func draftOf(j Job) (uuid.UUID, error) {
	var p struct {
		DraftID uuid.UUID `json:"draft_id"`
	}
	if err := json.Unmarshal(j.Payload, &p); err != nil || p.DraftID == uuid.Nil {
		return uuid.Nil, Permanent(fmt.Errorf("bad payload: %s", j.Payload))
	}
	return p.DraftID, nil
}

// permanentIdPError reports IdP errors a retry cannot fix.
func permanentIdPError(err error) bool {
	var taken *provisioners.UsernameTakenError
	return errors.As(err, &taken) || errors.Is(err, provisioners.ErrNoAccount) || errors.Is(err, provisioners.ErrLinkOnly)
}

// runJob is the onboarding.complete job.
func (s *ProvisionService) runJob(ctx context.Context, j Job) error {
	draftID, err := draftOf(j)
	if err != nil {
		return err
	}
	draft, err := s.drafts.Get(ctx, draftID)
	if err != nil {
		return Permanent(err)
	}
	if draft.State != domain.DraftStateProvisioning {
		// Done already, or cancelled while waiting.
		return nil
	}
	person, err := s.people.Get(ctx, draft.PersonID)
	if err != nil {
		return Permanent(err)
	}
	var deptGroup string
	if err := s.pool.QueryRow(ctx, `SELECT group_name FROM departments WHERE id = $1`, *draft.DepartmentID).Scan(&deptGroup); err != nil {
		return err
	}
	// A new person has no account yet, so every gated role waits unless an
	// earlier account already holds the qualifications.
	grantNow, pending, err := s.gate.Split(ctx, person.IdPUserID, draft.Roles)
	if err != nil {
		return err
	}
	gated := *draft
	gated.Roles = grantNow

	res := &CompleteResult{Jobs: []domain.ProvisioningJob{}, GroupsApplied: []string{}, PendingGrants: []PendingGrant{}}
	if s.idp != nil {
		extra := []string{deptGroup}
		if s.depts != nil {
			lead, err := s.depts.LeadGroupsFor(ctx, person.ID)
			if err != nil {
				return err
			}
			extra = append(extra, lead...)
		}
		pjob, idpRes, err := s.runIdP(ctx, &gated, person, extra, j.Attempts)
		res.Jobs = append(res.Jobs, pjob)
		if err != nil {
			if permanentIdPError(err) {
				return Permanent(err)
			}
			return err
		}
		res.GroupsApplied, res.AccountCreated = idpRes.GroupsApplied, idpRes.Created
		res.PasswordSetupEmailSent, res.PasswordSetupError = idpRes.PasswordSetupEmailSent, idpRes.PasswordSetupError
		res.PasswordSetupSkipped = !idpRes.Created || !s.sendSetup
	}
	if s.workspace != nil {
		if fresh, err := s.people.Get(ctx, person.ID); err == nil {
			person = fresh
		}
		if warn := s.workspace.Onboard(ctx, j.CreatedBy, draft, person, j.Attempts); warn != "" {
			res.Warnings = append(res.Warnings, warn)
		}
	}

	if res.Draft, err = s.drafts.Transition(ctx, draft.ID, domain.DraftStateActive); err != nil {
		return err
	}
	if err := s.people.SetStatus(ctx, person.ID, domain.PersonActive); err != nil {
		return err
	}
	pendingKeys := make([]string, 0, len(pending))
	for _, r := range pending {
		pendingKeys = append(pendingKeys, r.RoleName)
	}
	if len(pending) > 0 {
		if err := s.gate.RecordPending(ctx, person.ID, draft.ID, pending); err != nil {
			return err
		}
		if res.PendingGrants, err = s.gate.WaitingForPerson(ctx, person.ID); err != nil {
			return err
		}
	}
	if err := s.audit.Record(ctx, j.CreatedBy, "onboarding.complete", "employment_draft", draft.ID, map[string]any{
		"person_id":        person.ID,
		"groups":           res.GroupsApplied,
		"setup_email_sent": res.PasswordSetupEmailSent,
		"pending_roles":    pendingKeys,
		"attempt":          j.Attempts,
	}); err != nil {
		return err
	}
	res.OK = true
	return s.jobs.SetResult(ctx, j.ID, res)
}

// giveUp puts the onboarding back to "draft" after the last failed attempt.
func (s *ProvisionService) giveUp(ctx context.Context, j Job, reason string) {
	draftID, err := draftOf(j)
	if err != nil {
		return
	}
	_, _ = s.pool.Exec(ctx, `
UPDATE provisioning_jobs SET status = 'failed', message = $2, finished_at = now()
 WHERE draft_id = $1 AND status = 'running'`, draftID, "interrupted: "+reason)
	if d, err := s.drafts.Get(ctx, draftID); err == nil && d.State == domain.DraftStateProvisioning {
		_, _ = s.drafts.Transition(ctx, draftID, domain.DraftStateDraft)
	}
	_ = s.jobs.SetResult(ctx, j.ID, &CompleteResult{Error: reason, Jobs: []domain.ProvisioningJob{}, GroupsApplied: []string{}, PendingGrants: []PendingGrant{}})
	_ = s.audit.Record(ctx, j.CreatedBy, "onboarding.complete.failed", "employment_draft", draftID, map[string]any{"error": reason, "attempts": j.Attempts})
}

// Status returns the onboarding's latest completion job (nil if none).
func (s *ProvisionService) Status(ctx context.Context, draftID uuid.UUID) (*ProvisioningStatus, error) {
	var st ProvisioningStatus
	var result []byte
	err := s.pool.QueryRow(ctx, `
SELECT id, status, attempts, max_attempts, last_error, result, created_at FROM background_jobs
 WHERE kind = $1 AND payload->>'draft_id' = $2 ORDER BY created_at DESC LIMIT 1`, JobOnboardingComplete, draftID.String()).
		Scan(&st.JobID, &st.Status, &st.Attempts, &st.MaxAttempts, &st.Error, &result, &st.QueuedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, httperror.Internal(err)
	}
	if len(result) > 0 {
		var r CompleteResult
		if json.Unmarshal(result, &r) == nil {
			st.Result = &r
		}
	}
	return &st, nil
}

// RecoverStuck puts onboardings back to "draft" that are "provisioning"
// without a queued or running job (e.g. the job row was lost).
func (s *ProvisionService) RecoverStuck(ctx context.Context) (int, error) {
	rows, err := s.pool.Query(ctx, `
SELECT d.id FROM employment_drafts d
 WHERE d.state = 'provisioning' AND d.updated_at < now() - interval '10 minutes'
   AND NOT EXISTS (SELECT 1 FROM background_jobs j WHERE j.kind = $1 AND j.payload->>'draft_id' = d.id::text
                    AND j.status IN ('pending', 'running'))`, JobOnboardingComplete)
	if err != nil {
		return 0, err
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		if _, err := s.drafts.Transition(ctx, id, domain.DraftStateDraft); err != nil {
			continue
		}
		_, _ = s.pool.Exec(ctx, `UPDATE provisioning_jobs SET status = 'failed', message = 'interrupted', finished_at = now() WHERE draft_id = $1 AND status = 'running'`, id)
		_ = s.audit.Record(ctx, "system:recovery", "onboarding.complete.failed", "employment_draft", id, map[string]any{"error": "stuck in provisioning; put back to draft"})
	}
	return len(ids), rows.Err()
}

func (s *ProvisionService) runIdP(ctx context.Context, draft *domain.EmploymentDraft, person *domain.Person, extraGroups []string, attempt int) (domain.ProvisioningJob, provisioners.IdPResult, error) {
	job := s.beginJob(ctx, draft.ID, domain.TargetIdP, attempt)

	groups := append(make([]string, 0, len(draft.Roles)+len(extraGroups)), extraGroups...)
	for _, r := range draft.Roles {
		if r.GroupName != "" {
			groups = append(groups, r.GroupName)
		}
	}

	settings, err := s.settings.Get(ctx)
	if err != nil {
		s.finishJob(ctx, &job, domain.JobStatusFailed, err.Error(), "")
		return job, provisioners.IdPResult{}, err
	}
	username := firstNonEmpty(person.Username, UsernameFor(settings.UsernameTemplate, person))
	res, err := s.idp.Provision(ctx, provisioners.IdPUser{
		HRPersonID:  person.ID,
		ExternalID:  person.IdPUserID,
		Username:    username,
		DisplayName: person.DisplayName,
		FirstName:   person.FirstName,
		LastName:    person.LastName,
		Email:       person.Email,
		Attributes:  personAttributes(person),
		Groups:      groups,

		SendPasswordSetup: s.sendSetup,
	})
	if err != nil {
		var taken *provisioners.UsernameTakenError
		if errors.As(err, &taken) {
			err = fmt.Errorf("%w; change their nickname and try again", err)
		}
		s.finishJob(ctx, &job, domain.JobStatusFailed, err.Error(), "")
		return job, res, err
	}
	msg := ""
	switch {
	case res.PasswordSetupEmailSent:
		msg = "password_setup_email_sent"
	case res.PasswordSetupError != "":
		msg = "password_setup_email_failed: " + res.PasswordSetupError
	case res.Created:
		msg = "password_setup_skipped"
	}
	s.finishJob(ctx, &job, domain.JobStatusSucceeded, msg, res.ExternalID)
	if res.ExternalID != "" {
		if err := linkAccount(ctx, s.pool, person.ID, res.ExternalID, ""); err != nil {
			return job, res, err
		}
	}
	return job, res, nil
}

func (s *ProvisionService) beginJob(ctx context.Context, draftID uuid.UUID, target domain.ProvisionTarget, attempt int) domain.ProvisioningJob {
	if attempt < 1 {
		attempt = 1
	}
	job := domain.ProvisioningJob{
		ID:        uuid.New(),
		DraftID:   draftID,
		Target:    target,
		Status:    domain.JobStatusRunning,
		Attempt:   attempt,
		StartedAt: time.Now().UTC(),
	}
	// Best-effort insert; the job itself is tracked in background_jobs.
	_, _ = s.pool.Exec(ctx, `
INSERT INTO provisioning_jobs (id, draft_id, target, status, attempt, started_at)
VALUES ($1, $2, $3, $4, $5, $6)`, job.ID, job.DraftID, string(job.Target), string(job.Status), job.Attempt, job.StartedAt)
	return job
}

func (s *ProvisionService) finishJob(ctx context.Context, job *domain.ProvisioningJob, status domain.JobStatus, message, externalID string) {
	now := time.Now().UTC()
	job.Status = status
	job.Message = message
	job.ExternalID = externalID
	job.FinishedAt = &now
	_, _ = s.pool.Exec(ctx, `
UPDATE provisioning_jobs
   SET status = $1, message = NULLIF($2, ''), external_id = NULLIF($3, ''), finished_at = $4
 WHERE id = $5`, string(status), message, externalID, now, job.ID)
}

// ListJobs returns every provisioning_job for a draft, newest first. Job
// messages never contain passwords.
func (s *ProvisionService) ListJobs(ctx context.Context, draftID uuid.UUID) ([]domain.ProvisioningJob, error) {
	rows, err := s.pool.Query(ctx, `
SELECT id, draft_id, target, status, attempt, COALESCE(message, ''), started_at, finished_at, COALESCE(external_id, '')
FROM provisioning_jobs WHERE draft_id = $1 ORDER BY started_at DESC`, draftID)
	if err != nil {
		return nil, httperror.Internal(err)
	}
	defer rows.Close()
	out := make([]domain.ProvisioningJob, 0, 4)
	for rows.Next() {
		var j domain.ProvisioningJob
		if err := rows.Scan(&j.ID, &j.DraftID, &j.Target, &j.Status, &j.Attempt, &j.Message, &j.StartedAt, &j.FinishedAt, &j.ExternalID); err != nil {
			return nil, httperror.Internal(err)
		}
		out = append(out, j)
	}
	if err := rows.Err(); err != nil {
		return nil, httperror.Internal(err)
	}
	return out, nil
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
