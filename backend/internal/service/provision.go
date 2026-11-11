package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AGlElectronics/hr/backend/internal/audit"
	"github.com/AGlElectronics/hr/backend/internal/domain"
	"github.com/AGlElectronics/hr/backend/internal/httperror"
	"github.com/AGlElectronics/hr/backend/internal/provisioners"
)

// ProvisionService drives the provisioning state transitions. It is the only
// collaborator that touches the provisioners package — everything else goes
// through here so swapping the Fake implementations for real ones (Keycloak,
// Google) only changes constructor wiring.
type ProvisionService struct {
	pool      *pgxpool.Pool
	drafts    *DraftService
	people    *PeopleService
	idp       provisioners.IdPProvisioner // nil when IDP_PROVISIONER_MODE=off
	audit     *audit.Writer
	settings  *SettingsService
	gate      *RoleGateService // nil = no training requirements
}

// NewProvisionService wires a ProvisionService. idp is nil in off mode.
func NewProvisionService(pool *pgxpool.Pool, drafts *DraftService, people *PeopleService, idp provisioners.IdPProvisioner, aud *audit.Writer, settings *SettingsService) *ProvisionService {
	return &ProvisionService{pool: pool, drafts: drafts, people: people, idp: idp, audit: aud, settings: settings}
}

// SetRoleGate holds roles with unmet training requirements back at Complete.
func (s *ProvisionService) SetRoleGate(g *RoleGateService) { s.gate = g }

func (s *ProvisionService) runIdPWith(ctx context.Context, draft *domain.EmploymentDraft, person *domain.Person, extraGroups []string, passwordSetup bool) (domain.ProvisioningJob, provisioners.IdPResult, error) {
	job := s.beginJob(ctx, draft.ID, domain.TargetIdP)

	groups := append(make([]string, 0, len(draft.Roles)+len(extraGroups)), extraGroups...)
	for _, r := range draft.Roles {
		if r.GroupName != "" {
			groups = append(groups, r.GroupName)
		} else {
			groups = append(groups, r.RoleName)
		}
	}

	settings, err := s.settings.Get(ctx)
	if err != nil {
		s.finishJob(ctx, &job, domain.JobStatusFailed, err.Error(), "")
		return job, provisioners.IdPResult{}, err
	}
	ext, err := latestExternalID(ctx, s.pool, person.ID)
	if err != nil {
		s.finishJob(ctx, &job, domain.JobStatusFailed, err.Error(), "")
		return job, provisioners.IdPResult{}, err
	}
	username := firstNonEmpty(person.Username, UsernameFor(settings.UsernameTemplate, person))
	res, err := s.idp.Provision(ctx, provisioners.IdPUser{
		HRPersonID:  person.ID,
		ExternalID:  ext,
		Username:    username,
		DisplayName: person.DisplayName,
		FirstName:   person.FirstName,
		LastName:    person.LastName,
		Email:       person.Email,
		Attributes:  personAttributes(person),
		Groups:      groups,

		SendPasswordSetup: passwordSetup,
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
	case res.TemporaryPassword != "":
		// Fallback: surface the one-time password on the job so organizers
		// can hand it over (Keycloak marks it temporary / must change).
		msg = "temporary_password=" + res.TemporaryPassword
		if res.PasswordSetupError != "" {
			msg += " (set-password email failed: " + res.PasswordSetupError + ")"
		}
	}
	s.finishJob(ctx, &job, domain.JobStatusSucceeded, msg, res.ExternalID)
	return job, res, nil
}

// CompleteResult is what the onboarding overview shows after "Complete".
type CompleteResult struct {
	OK                     bool                     `json:"ok"`
	Error                  string                   `json:"error,omitempty"`
	Draft                  *domain.EmploymentDraft  `json:"draft"`
	Jobs                   []domain.ProvisioningJob `json:"jobs"`
	GroupsApplied          []string                 `json:"groups_applied"`
	AccountCreated         bool                     `json:"account_created"`
	PasswordSetupEmailSent bool                     `json:"password_setup_email_sent"`
	PasswordSetupError     string                   `json:"password_setup_error,omitempty"`
	TemporaryPassword      string                   `json:"temporary_password,omitempty"`
	// PendingGrants are roles left out until the person finishes training.
	PendingGrants []PendingGrant `json:"pending_grants"`
}

// Complete finishes an onboarding draft in one step after HR reviewed it:
// create the IdP account (department group + role groups), have the IdP
// email a set-password link, provision the workspace, and mark both the
// draft and the person active. On connector failure the draft goes back to
// "draft" so HR can fix things and retry.
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
	var deptGroup string
	if err := s.pool.QueryRow(ctx, `SELECT group_name FROM departments WHERE id = $1`, *draft.DepartmentID).Scan(&deptGroup); err != nil {
		return nil, httperror.Internal(err)
	}

	// A new person has no account yet, so every gated role waits unless an
	// earlier account already holds the qualifications.
	ext, err := latestExternalID(ctx, s.pool, person.ID)
	if err != nil {
		return nil, err
	}
	grantNow, pending, err := s.gate.Split(ctx, ext, draft.Roles)
	if err != nil {
		return nil, err
	}
	gated := *draft
	gated.Roles = grantNow

	if _, err := s.drafts.Transition(ctx, draft.ID, domain.DraftStateProvisioning); err != nil {
		return nil, err
	}
	jobs := []domain.ProvisioningJob{}
	var idpRes provisioners.IdPResult
	var idpErr, wsErr error
	if s.idp != nil {
		var idpJob domain.ProvisioningJob
		idpJob, idpRes, idpErr = s.runIdPWith(ctx, &gated, person, []string{deptGroup}, true)
		jobs = append(jobs, idpJob)
	}

	res := &CompleteResult{Jobs: jobs, GroupsApplied: idpRes.GroupsApplied, AccountCreated: idpRes.Created,
		PasswordSetupEmailSent: idpRes.PasswordSetupEmailSent, PasswordSetupError: idpRes.PasswordSetupError,
		TemporaryPassword: idpRes.TemporaryPassword, PendingGrants: []PendingGrant{}}

	if failure := firstNonEmpty(errString(idpErr), errString(wsErr)); failure != "" {
		res.Error = failure
		res.Draft, err = s.drafts.Transition(ctx, draft.ID, domain.DraftStateDraft)
		if err != nil {
			return nil, err
		}
		_ = s.audit.Record(ctx, actor, "onboarding.complete.failed", "employment_draft", draft.ID, map[string]any{"error": failure})
		return res, nil
	}

	if res.Draft, err = s.drafts.Transition(ctx, draft.ID, domain.DraftStateActive); err != nil {
		return nil, err
	}
	if err := s.people.SetStatus(ctx, person.ID, domain.PersonActive); err != nil {
		return nil, err
	}
	pendingKeys := make([]string, 0, len(pending))
	for _, r := range pending {
		pendingKeys = append(pendingKeys, r.RoleName)
	}
	if len(pending) > 0 {
		if err := s.gate.RecordPending(ctx, person.ID, draft.ID, pending); err != nil {
			return nil, err
		}
		if res.PendingGrants, err = s.gate.WaitingForPerson(ctx, person.ID); err != nil {
			return nil, err
		}
	}
	if err := s.audit.Record(ctx, actor, "onboarding.complete", "employment_draft", draft.ID, map[string]any{
		"person_id":        person.ID,
		"groups":           idpRes.GroupsApplied,
		"setup_email_sent": idpRes.PasswordSetupEmailSent,
		"pending_roles":    pendingKeys,
	}); err != nil {
		return nil, err
	}
	res.OK = true
	return res, nil
}

func (s *ProvisionService) beginJob(ctx context.Context, draftID uuid.UUID, target domain.ProvisionTarget) domain.ProvisioningJob {
	job := domain.ProvisioningJob{
		ID:        uuid.New(),
		DraftID:   draftID,
		Target:    target,
		Status:    domain.JobStatusRunning,
		Attempt:   1,
		StartedAt: time.Now().UTC(),
	}
	// Best-effort insert; the parent operation keeps running even on insert
	// failure (we at least have the audit event). Logging happens in the
	// caller when it tries to render the response.
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

// ListJobs returns every provisioning_job for a draft, newest first.
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

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
