package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AGlElectronics/hr/backend/internal/audit"
	"github.com/AGlElectronics/hr/backend/internal/domain"
	"github.com/AGlElectronics/hr/backend/internal/httperror"
	"github.com/AGlElectronics/hr/backend/internal/mail"
	"github.com/AGlElectronics/hr/backend/internal/provisioners"
)

// WorkspaceService is the Google Workspace connector: every onboarded
// person gets an account named after HR's organization email, departments
// are mirrored as Google Groups whose members follow HR, offboarding
// suspends the account, and a daily task deletes suspended accounts after
// the configured time (HR admins are reminded first so they can clear out
// data).
type WorkspaceService struct {
	pool     *pgxpool.Pool
	settings *SettingsService
	audit    *audit.Writer
	mailer   mail.Sender // nil: the person is not emailed
	notify   *AdminNotifier
	open     provisioners.WorkspaceFactory
	now      func() time.Time
}

// NewWorkspaceService wires the connector; open nil means no Workspace
// client is available (the connector stays off).
func NewWorkspaceService(pool *pgxpool.Pool, settings *SettingsService, aud *audit.Writer, mailer mail.Sender, notify *AdminNotifier, open provisioners.WorkspaceFactory) *WorkspaceService {
	return &WorkspaceService{pool: pool, settings: settings, audit: aud, mailer: mailer, notify: notify, open: open, now: time.Now}
}

// WorkspaceAccount is one row of workspace_accounts.
type WorkspaceAccount struct {
	ID           uuid.UUID  `json:"id"`
	PersonID     *uuid.UUID `json:"person_id,omitempty"`
	PrimaryEmail string     `json:"primary_email"`
	DisplayName  string     `json:"display_name"`
	Status       string     `json:"status"`
	CreatedAt    time.Time  `json:"created_at"`
	SuspendedAt  *time.Time `json:"suspended_at,omitempty"`
	DeleteAfter  *time.Time `json:"delete_after,omitempty"`
	RemindedAt   *time.Time `json:"reminded_at,omitempty"`
	DeletedAt    *time.Time `json:"deleted_at,omitempty"`
	LastError    string     `json:"last_error,omitempty"`
}

// directory opens the Workspace directory when the connector is on.
func (w *WorkspaceService) directory(ctx context.Context) (provisioners.WorkspaceDirectory, *Settings, error) {
	if w == nil || w.open == nil {
		return nil, nil, nil
	}
	st, err := w.settings.Get(ctx)
	if err != nil || !st.Workspace.Enabled {
		return nil, st, err
	}
	dir, err := w.open(ctx, provisioners.WorkspaceTarget{Domain: w.domain(st), AdminEmail: st.Workspace.AdminEmail, OrgUnit: st.Workspace.OrgUnit})
	return dir, st, err
}

func (w *WorkspaceService) domain(st *Settings) string {
	return firstNonEmpty(st.Workspace.Domain, st.OrgEmailDomain)
}

// GroupEmail is the Google Group of a department ("dept-security@domain").
func GroupEmail(deptGroup, domain string) string { return deptGroup + "@" + domain }

// Onboard creates (or re-activates) the person's Workspace account, adds
// them to their department's group and emails them how to set their
// password. It returns a warning instead of failing the onboarding.
func (w *WorkspaceService) Onboard(ctx context.Context, actor string, d *domain.EmploymentDraft, p *domain.Person, attempt int) string {
	dir, st, err := w.directory(ctx)
	if err != nil {
		return "Google Workspace: " + err.Error()
	}
	if dir == nil {
		return ""
	}
	dom := w.domain(st)
	if dom == "" {
		return "Google Workspace: set the Workspace domain or the organization email domain in HR settings."
	}
	email := strings.ToLower(p.OrgEmail)
	if email == "" {
		local := RenderTemplate(st.OrgEmailTemplate, p)
		if local == "" {
			return "Google Workspace: could not build an organization email for " + p.DisplayName + "; set it on their profile and save."
		}
		email = local + "@" + dom
		if _, err := w.pool.Exec(ctx, `UPDATE people SET org_email = $2, updated_at = now() WHERE id = $1 AND org_email IS NULL`, p.ID, email); err != nil {
			return "Google Workspace: " + fmt.Sprintf("the organization email %s is already used by someone else", email)
		}
	}
	if !strings.HasSuffix(email, "@"+dom) {
		return "Google Workspace: " + email + " is not in the Workspace domain " + dom + "; no account was created."
	}
	job := domain.ProvisioningJob{ID: uuid.New(), DraftID: d.ID, Target: domain.TargetWorkspace, Status: domain.JobStatusRunning, Attempt: max(attempt, 1), StartedAt: w.now().UTC()}
	_, _ = w.pool.Exec(ctx, `INSERT INTO provisioning_jobs (id, draft_id, target, status, attempt, started_at) VALUES ($1, $2, 'workspace', 'running', $3, $4)`,
		job.ID, job.DraftID, job.Attempt, job.StartedAt)
	finish := func(status domain.JobStatus, msg string) {
		_, _ = w.pool.Exec(ctx, `UPDATE provisioning_jobs SET status = $2, message = NULLIF($3, ''), external_id = $4, finished_at = now() WHERE id = $1`,
			job.ID, string(status), msg, email)
	}

	created := false
	existing, err := dir.GetUser(ctx, email)
	switch {
	case errors.Is(err, provisioners.ErrWorkspaceNotFound):
		err = dir.CreateUser(ctx, provisioners.WorkspaceUser{PrimaryEmail: email, GivenName: p.FirstName, FamilyName: p.LastName,
			OrgUnitPath: st.Workspace.OrgUnit, RecoveryEmail: p.Email})
		created = err == nil
	case err == nil && existing.Suspended:
		err = dir.SetSuspended(ctx, email, false)
	}
	if err != nil {
		finish(domain.JobStatusFailed, err.Error())
		return "Google Workspace: could not set up " + email + ": " + err.Error()
	}
	if _, err := w.pool.Exec(ctx, `
INSERT INTO workspace_accounts (person_id, primary_email, display_name, status) VALUES ($1, $2, $3, 'active')
ON CONFLICT (primary_email) DO UPDATE SET person_id = EXCLUDED.person_id, display_name = EXCLUDED.display_name, status = 'active',
       suspended_at = NULL, delete_after = NULL, reminded_at = NULL, deleted_at = NULL, last_error = ''`,
		p.ID, email, p.DisplayName); err != nil {
		finish(domain.JobStatusFailed, err.Error())
		return "Google Workspace: " + err.Error()
	}
	finish(domain.JobStatusSucceeded, map[bool]string{true: "workspace_account_created", false: "workspace_account_linked"}[created])
	_ = w.audit.Record(ctx, actor, "workspace.create", "person", p.ID, map[string]any{"email": email, "created": created})
	warning := ""
	if d.DepartmentID != nil {
		if err := w.SyncDepartment(ctx, *d.DepartmentID); err != nil {
			warning = "Google Workspace: account ready, but the department group could not be updated: " + err.Error()
		}
	}
	if created {
		if err := w.sendWelcome(ctx, st, p, email); err != nil {
			warning = strings.TrimSpace(warning + " Google Workspace: account " + email + " created, but HR could not email them how to sign in: " + err.Error())
		}
	}
	return warning
}

func (w *WorkspaceService) sendWelcome(ctx context.Context, st *Settings, p *domain.Person, email string) error {
	if w.mailer == nil {
		return mail.ErrNotConfigured
	}
	org := firstNonEmpty(st.OrganizationName, "the team")
	name := firstNonEmpty(p.Nickname, p.FirstName, p.DisplayName)
	body := fmt.Sprintf("Hi %s,\n\nYou now have a Google Workspace account at %s:\n\n  %s\n\n"+
		"To choose your password, open https://accounts.google.com/signin/recovery, enter %s and follow the steps; "+
		"Google sends the code to this email address (%s).\n\nWelcome!\n", name, org, email, email, p.Email)
	return w.mailer.Send(ctx, mail.Message{To: p.Email, Subject: "Your " + org + " Google Workspace account", Body: body})
}

// SyncDepartment makes the department's Google Group hold exactly the
// Workspace accounts of its active members.
func (w *WorkspaceService) SyncDepartment(ctx context.Context, deptID uuid.UUID) error {
	dir, st, err := w.directory(ctx)
	if err != nil || dir == nil {
		return err
	}
	var name, group string
	if err := w.pool.QueryRow(ctx, `SELECT name, group_name FROM departments WHERE id = $1`, deptID).Scan(&name, &group); err != nil {
		return err
	}
	groupEmail := GroupEmail(group, w.domain(st))
	if err := dir.EnsureGroup(ctx, groupEmail, name); err != nil {
		return err
	}
	rows, err := w.pool.Query(ctx, `
SELECT DISTINCT lower(wa.primary_email) FROM workspace_accounts wa
  JOIN people p ON p.id = wa.person_id AND p.status = 'active'
  JOIN employment_drafts d ON d.person_id = p.id AND d.state = 'active' AND d.department_id = $1
 WHERE wa.status = 'active'`, deptID)
	if err != nil {
		return err
	}
	want := map[string]bool{}
	for rows.Next() {
		var e string
		if err := rows.Scan(&e); err != nil {
			rows.Close()
			return err
		}
		want[e] = true
	}
	rows.Close()
	have, err := dir.ListMembers(ctx, groupEmail)
	if err != nil {
		return err
	}
	has := map[string]bool{}
	for _, m := range have {
		m = strings.ToLower(m)
		has[m] = true
		if !want[m] {
			if err := dir.RemoveMember(ctx, groupEmail, m); err != nil {
				return err
			}
		}
	}
	for m := range want {
		if !has[m] {
			if err := dir.AddMember(ctx, groupEmail, m); err != nil {
				return err
			}
		}
	}
	return nil
}

// SyncAllDepartments mirrors every department (maintenance).
func (w *WorkspaceService) SyncAllDepartments(ctx context.Context) error {
	if dir, _, err := w.directory(ctx); err != nil || dir == nil {
		return err
	}
	rows, err := w.pool.Query(ctx, `SELECT id FROM departments WHERE archived_at IS NULL`)
	if err != nil {
		return err
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	var errs []string
	for _, id := range ids {
		if err := w.SyncDepartment(ctx, id); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// Offboard suspends the person's Workspace account and schedules its
// deletion. It returns a warning instead of failing the offboarding.
func (w *WorkspaceService) Offboard(ctx context.Context, actor string, p *domain.Person) string {
	dir, st, err := w.directory(ctx)
	if err != nil {
		return "Google Workspace: " + err.Error()
	}
	if dir == nil {
		return ""
	}
	rows, err := w.pool.Query(ctx, `SELECT id, primary_email FROM workspace_accounts WHERE person_id = $1 AND status = 'active'`, p.ID)
	if err != nil {
		return "Google Workspace: " + err.Error()
	}
	type acct struct {
		id    uuid.UUID
		email string
	}
	var accts []acct
	for rows.Next() {
		var a acct
		if err := rows.Scan(&a.id, &a.email); err == nil {
			accts = append(accts, a)
		}
	}
	rows.Close()
	var warnings []string
	deleteAfter := w.now().Add(time.Duration(st.Workspace.DeleteAfterDays) * 24 * time.Hour)
	for _, a := range accts {
		err := dir.SetSuspended(ctx, a.email, true)
		if err != nil && !errors.Is(err, provisioners.ErrWorkspaceNotFound) {
			warnings = append(warnings, "Google Workspace: could not suspend "+a.email+": "+err.Error()+". Suspend it in the Google Admin console.")
			_, _ = w.pool.Exec(ctx, `UPDATE workspace_accounts SET last_error = $2 WHERE id = $1`, a.id, err.Error())
			continue
		}
		_, _ = w.pool.Exec(ctx, `
UPDATE workspace_accounts SET status = 'suspended', suspended_at = $2, delete_after = $3, display_name = $4, last_error = '' WHERE id = $1`,
			a.id, w.now(), deleteAfter, p.DisplayName)
		_ = w.audit.Record(ctx, actor, "workspace.suspend", "person", p.ID, map[string]any{"email": a.email, "delete_after": deleteAfter.Format(time.DateOnly)})
	}
	return strings.Join(warnings, " ")
}

// Cleanup reminds HR admins of upcoming deletions and deletes suspended
// accounts whose time is up (daily maintenance).
func (w *WorkspaceService) Cleanup(ctx context.Context) error {
	dir, st, err := w.directory(ctx)
	if err != nil || dir == nil {
		return err
	}
	now := w.now()
	remindBy := now.Add(time.Duration(st.Workspace.ReminderDays) * 24 * time.Hour)
	due, err := w.list(ctx, `status = 'suspended' AND reminded_at IS NULL AND delete_after <= $1`, remindBy)
	if err != nil {
		return err
	}
	if len(due) > 0 {
		var b strings.Builder
		b.WriteString("Hi,\n\nThese Google Workspace accounts of offboarded people will be deleted soon, with their mail and files:\n\n")
		for _, a := range due {
			fmt.Fprintf(&b, "  %s (%s): deleted on or after %s\n", a.PrimaryEmail, a.DisplayName, a.DeleteAfter.Format("2 January 2006"))
		}
		b.WriteString("\nIf you need anything from them, transfer it now in the Google Admin console (Data transfer). HR deletes them on the date shown.\n")
		if err := w.notify.Send(ctx, "Google Workspace accounts will be deleted soon", b.String()); err != nil {
			return fmt.Errorf("workspace: could not remind HR admins, nothing deleted: %w", err)
		}
		ids := make([]uuid.UUID, 0, len(due))
		for _, a := range due {
			ids = append(ids, a.ID)
		}
		if _, err := w.pool.Exec(ctx, `UPDATE workspace_accounts SET reminded_at = $2 WHERE id = ANY($1)`, ids, now); err != nil {
			return err
		}
	}
	// Deleted only once the reminder went out at least ReminderDays ago.
	expired, err := w.list(ctx, `status = 'suspended' AND reminded_at IS NOT NULL AND delete_after <= now()
   AND reminded_at <= now() - make_interval(days => $1)`, st.Workspace.ReminderDays)
	if err != nil {
		return err
	}
	var errs []string
	for _, a := range expired {
		if err := w.delete(ctx, dir, "system:workspace", a); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func (w *WorkspaceService) delete(ctx context.Context, dir provisioners.WorkspaceDirectory, actor string, a WorkspaceAccount) error {
	err := dir.DeleteUser(ctx, a.PrimaryEmail)
	if err != nil && !errors.Is(err, provisioners.ErrWorkspaceNotFound) {
		_, _ = w.pool.Exec(ctx, `UPDATE workspace_accounts SET last_error = $2 WHERE id = $1`, a.ID, err.Error())
		return fmt.Errorf("delete %s: %w", a.PrimaryEmail, err)
	}
	if _, err := w.pool.Exec(ctx, `UPDATE workspace_accounts SET status = 'deleted', deleted_at = now(), last_error = '' WHERE id = $1`, a.ID); err != nil {
		return err
	}
	entity := a.ID
	if a.PersonID != nil {
		entity = *a.PersonID
	}
	return w.audit.Record(ctx, actor, "workspace.delete", "person", entity, map[string]any{"email": a.PrimaryEmail})
}

// DeleteNow deletes a suspended account before its scheduled date.
func (w *WorkspaceService) DeleteNow(ctx context.Context, actor string, id uuid.UUID) (*WorkspaceAccount, error) {
	dir, _, err := w.directory(ctx)
	if err != nil {
		return nil, httperror.New(httperror.CodeUpstream, "Google Workspace: "+err.Error())
	}
	if dir == nil {
		return nil, httperror.Conflict("the Google Workspace connector is off")
	}
	accts, err := w.list(ctx, `id = $1`, id)
	if err != nil {
		return nil, httperror.Internal(err)
	}
	if len(accts) == 0 {
		return nil, httperror.NotFound("workspace account")
	}
	if accts[0].Status != "suspended" {
		return nil, httperror.Conflict("only suspended accounts of offboarded people can be deleted here")
	}
	if err := w.delete(ctx, dir, actor, accts[0]); err != nil {
		return nil, httperror.New(httperror.CodeUpstream, err.Error())
	}
	out, err := w.list(ctx, `id = $1`, id)
	if err != nil || len(out) == 0 {
		return nil, httperror.Internal(err)
	}
	return &out[0], nil
}

// List returns Workspace accounts, optionally by status.
func (w *WorkspaceService) List(ctx context.Context, status string) ([]WorkspaceAccount, error) {
	return w.list(ctx, `($1 = '' OR status = $1)`, status)
}

func (w *WorkspaceService) list(ctx context.Context, where string, arg any) ([]WorkspaceAccount, error) {
	rows, err := w.pool.Query(ctx, `
SELECT id, person_id, primary_email, display_name, status, created_at, suspended_at, delete_after, reminded_at, deleted_at, last_error
  FROM workspace_accounts WHERE `+where+` ORDER BY COALESCE(delete_after, created_at)`, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []WorkspaceAccount{}
	for rows.Next() {
		var a WorkspaceAccount
		if err := rows.Scan(&a.ID, &a.PersonID, &a.PrimaryEmail, &a.DisplayName, &a.Status, &a.CreatedAt, &a.SuspendedAt,
			&a.DeleteAfter, &a.RemindedAt, &a.DeletedAt, &a.LastError); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	if errors.Is(rows.Err(), pgx.ErrNoRows) {
		return out, nil
	}
	return out, rows.Err()
}
