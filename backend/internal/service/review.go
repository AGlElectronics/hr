package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/AGlElectronics/hr/backend/internal/actor"
	"github.com/AGlElectronics/hr/backend/internal/audit"
	"github.com/AGlElectronics/hr/backend/internal/domain"
	"github.com/AGlElectronics/hr/backend/internal/httperror"
	"github.com/AGlElectronics/hr/backend/internal/provisioners"
)

// Review item sources and states.
const (
	ReviewSourceScan   = "idp_scan"
	ReviewSourceSelf   = "self_service"
	ReviewSourceImport = "import"

	ReviewPending  = "pending"
	ReviewReady    = "ready"
	ReviewAccepted = "accepted"
	ReviewMerged   = "merged"
	ReviewRejected = "rejected"
)

// ReviewData are the profile fields a review item carries besides names,
// email and phone.
type ReviewData struct {
	DateOfBirth      string                  `json:"date_of_birth,omitempty"`
	Address          domain.Address          `json:"address"`
	EmergencyContact domain.EmergencyContact `json:"emergency_contact"`
	SocialHandles    map[string]string       `json:"social_handles,omitempty"`
	OrgEmail         string                  `json:"org_email,omitempty"`
	Notes            string                  `json:"notes,omitempty"`
	// Roles are role keys an import row asked for (shown to the reviewer).
	Roles []string `json:"roles,omitempty"`
	// Warnings are problems the importer found in the row ("phone stored
	// as a number lost its leading zero").
	Warnings []string `json:"warnings,omitempty"`
}

// ReviewItem is a draft person waiting for a human decision.
type ReviewItem struct {
	ID            uuid.UUID   `json:"id"`
	Source        string      `json:"source"`
	Status        string      `json:"status"`
	IdPUserID     string      `json:"idp_user_id,omitempty"`
	Email         string      `json:"email"`
	EmailVerified bool        `json:"email_verified"`
	FirstName     string      `json:"first_name"`
	LastName      string      `json:"last_name"`
	Nickname      string      `json:"nickname"`
	Phone         string      `json:"phone"`
	Data          ReviewData  `json:"data"`
	DepartmentID  *uuid.UUID  `json:"department_id,omitempty"`
	BundleIDs     []uuid.UUID `json:"bundle_ids"`
	// MatchPersonID is an existing person with the same email: verified
	// (the IdP verified the address) or possible (not verified). It is a
	// hint for merging, never applied automatically.
	MatchPersonID *uuid.UUID `json:"match_person_id,omitempty"`
	MatchKind     string     `json:"match_kind,omitempty"`
	ImportBatchID *uuid.UUID `json:"import_batch_id,omitempty"`
	ExternalRef   string     `json:"external_ref,omitempty"`
	PersonID      *uuid.UUID `json:"person_id,omitempty"`
	DraftID       *uuid.UUID `json:"draft_id,omitempty"`
	DecidedBy     string     `json:"decided_by,omitempty"`
	DecidedAt     *time.Time `json:"decided_at,omitempty"`
	DecisionNote  string     `json:"decision_note,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// ReviewService runs the review queue.
type ReviewService struct {
	pool       *pgxpool.Pool
	audit      *audit.Writer
	people     *PeopleService
	onboarding *OnboardingService
	drafts     *DraftService
	prov       *ProvisionService
	lifecycle  *LifecycleService
	scanner    provisioners.UserScanner    // nil: no scan
	accounts   provisioners.AccountManager // nil: reject cannot disable
	linkOnly   bool
	logger     zerolog.Logger
}

// NewReviewService wires a ReviewService; scanner and accounts may be nil.
func NewReviewService(pool *pgxpool.Pool, aud *audit.Writer, people *PeopleService, onboarding *OnboardingService, drafts *DraftService,
	prov *ProvisionService, lifecycle *LifecycleService, scanner provisioners.UserScanner, accounts provisioners.AccountManager, linkOnly bool, logger zerolog.Logger) *ReviewService {
	return &ReviewService{pool: pool, audit: aud, people: people, onboarding: onboarding, drafts: drafts, prov: prov, lifecycle: lifecycle,
		scanner: scanner, accounts: accounts, linkOnly: linkOnly, logger: logger.With().Str("component", "review").Logger()}
}

const reviewCols = `id, source, status, COALESCE(idp_user_id, ''), email, email_verified, first_name, last_name, nickname, phone, data,
department_id, bundle_ids, match_person_id, match_kind, import_batch_id, external_ref, person_id, draft_id, decided_by, decided_at,
decision_note, created_at, updated_at`

func scanReview(row rowScanner) (*ReviewItem, error) {
	it := &ReviewItem{}
	var data, bundles []byte
	if err := row.Scan(&it.ID, &it.Source, &it.Status, &it.IdPUserID, &it.Email, &it.EmailVerified, &it.FirstName, &it.LastName,
		&it.Nickname, &it.Phone, &data, &it.DepartmentID, &bundles, &it.MatchPersonID, &it.MatchKind, &it.ImportBatchID, &it.ExternalRef,
		&it.PersonID, &it.DraftID, &it.DecidedBy, &it.DecidedAt, &it.DecisionNote, &it.CreatedAt, &it.UpdatedAt); err != nil {
		return nil, err
	}
	_ = json.Unmarshal(data, &it.Data)
	it.BundleIDs = []uuid.UUID{}
	_ = json.Unmarshal(bundles, &it.BundleIDs)
	return it, nil
}

// ReviewFilter narrows the queue.
type ReviewFilter struct {
	Status string // "" = open (pending and ready)
	Source string
	Query  string
	// DepartmentIDs, when not nil, limits the queue to these departments
	// (department leads).
	DepartmentIDs []uuid.UUID
	ImportBatchID uuid.UUID
	Limit, Offset int
}

// List returns one page of the queue, oldest first.
func (s *ReviewService) List(ctx context.Context, f ReviewFilter) (*Page[ReviewItem], error) {
	f.Limit, f.Offset = NormalizePaging(f.Limit, f.Offset)
	scoped := f.DepartmentIDs != nil
	if f.DepartmentIDs == nil {
		f.DepartmentIDs = []uuid.UUID{}
	}
	q := "%" + strings.ToLower(strings.TrimSpace(f.Query)) + "%"
	rows, err := s.pool.Query(ctx, `SELECT `+reviewCols+`, count(*) OVER () FROM review_items
 WHERE (($1 = '' AND status IN ('pending', 'ready')) OR status = $1)
   AND ($2 = '' OR source = $2)
   AND ($3 = '%%' OR lower(email) LIKE $3 OR lower(first_name || ' ' || last_name) LIKE $3 OR lower(nickname) LIKE $3)
   AND (NOT $4 OR department_id = ANY($5))
   AND ($6 = '00000000-0000-0000-0000-000000000000'::uuid OR import_batch_id = $6)
 ORDER BY status = 'ready' DESC, created_at, id
 LIMIT $7 OFFSET $8`, strings.TrimSpace(f.Status), strings.TrimSpace(f.Source), q, scoped, f.DepartmentIDs, f.ImportBatchID, f.Limit, f.Offset)
	if err != nil {
		return nil, httperror.Internal(err)
	}
	defer rows.Close()
	out := &Page[ReviewItem]{Items: []ReviewItem{}, Limit: f.Limit, Offset: f.Offset}
	for rows.Next() {
		var total int
		it := &ReviewItem{}
		var data, bundles []byte
		if err := rows.Scan(&it.ID, &it.Source, &it.Status, &it.IdPUserID, &it.Email, &it.EmailVerified, &it.FirstName, &it.LastName,
			&it.Nickname, &it.Phone, &data, &it.DepartmentID, &bundles, &it.MatchPersonID, &it.MatchKind, &it.ImportBatchID, &it.ExternalRef,
			&it.PersonID, &it.DraftID, &it.DecidedBy, &it.DecidedAt, &it.DecisionNote, &it.CreatedAt, &it.UpdatedAt, &total); err != nil {
			return nil, httperror.Internal(err)
		}
		_ = json.Unmarshal(data, &it.Data)
		it.BundleIDs = []uuid.UUID{}
		_ = json.Unmarshal(bundles, &it.BundleIDs)
		out.Total = total
		out.Items = append(out.Items, *it)
	}
	return out, rows.Err()
}

// Get returns one item.
func (s *ReviewService) Get(ctx context.Context, id uuid.UUID) (*ReviewItem, error) {
	it, err := scanReview(s.pool.QueryRow(ctx, `SELECT `+reviewCols+` FROM review_items WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, httperror.NotFound("review item")
	}
	if err != nil {
		return nil, httperror.Internal(err)
	}
	return it, nil
}

func (s *ReviewService) open(ctx context.Context, id uuid.UUID) (*ReviewItem, error) {
	it, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if it.Status != ReviewPending && it.Status != ReviewReady {
		return nil, httperror.Conflict("this item was already " + it.Status)
	}
	return it, nil
}

// matchPerson finds an existing person with the email (private or
// organization address).
func (s *ReviewService) matchPerson(ctx context.Context, email string) (*uuid.UUID, error) {
	if email == "" {
		return nil, nil
	}
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `SELECT id FROM people WHERE lower(email) = lower($1) OR lower(COALESCE(org_email, '')) = lower($1) LIMIT 1`, email).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &id, nil
}

// ScanResult counts what a scan did.
type ScanResult struct {
	Seen    int `json:"seen"`
	Created int `json:"created"`
	Linked  int `json:"linked"`
}

// Scan pages through the IdP and queues every enabled account that has no
// hrPersonId and no HR record yet. An imported row with the same verified
// email is linked to the account; unverified addresses are only marked as
// a possible match.
func (s *ReviewService) Scan(ctx context.Context) (*ScanResult, error) {
	if s.scanner == nil {
		return nil, httperror.Conflict("the identity provider cannot be scanned (IDP_PROVISIONER_MODE=off or no Keycloak)")
	}
	res := &ScanResult{}
	err := s.scanner.ScanUsers(ctx, func(u provisioners.UserInfo) error {
		res.Seen++
		if !u.Enabled || u.Attributes[provisioners.AttrHRPersonID] != "" {
			return nil
		}
		var known bool
		if err := s.pool.QueryRow(ctx, `
SELECT EXISTS (SELECT 1 FROM people WHERE idp_user_id = $1 OR idp_subject = $1)
    OR EXISTS (SELECT 1 FROM review_items WHERE idp_user_id = $1 OR (idp_subject = $1 AND status IN ('pending', 'ready')))`, u.ID).Scan(&known); err != nil {
			return err
		}
		if known {
			return nil
		}
		if u.EmailVerified && u.Email != "" {
			tag, err := s.pool.Exec(ctx, `
UPDATE review_items SET idp_user_id = $1, idp_subject = COALESCE(idp_subject, $1), email_verified = true, match_kind = 'verified_email',
       updated_at = now()
 WHERE id = (SELECT id FROM review_items WHERE source = 'import' AND idp_user_id IS NULL AND status IN ('pending', 'ready')
              AND lower(email) = lower($2) ORDER BY created_at LIMIT 1)`, u.ID, u.Email)
			if err != nil {
				return err
			}
			if tag.RowsAffected() > 0 {
				res.Linked++
				return nil
			}
		}
		match, err := s.matchPerson(ctx, u.Email)
		if err != nil {
			return err
		}
		kind := ""
		switch {
		case match != nil && u.EmailVerified:
			kind = "verified_email"
		case match != nil:
			kind = "possible"
		default:
			var imported bool
			_ = s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM review_items WHERE source = 'import' AND lower(email) = lower($1)
                                      AND status IN ('pending', 'ready'))`, u.Email).Scan(&imported)
			if imported && u.Email != "" {
				kind = "possible"
			}
		}
		if _, err := s.pool.Exec(ctx, `
INSERT INTO review_items (source, idp_user_id, idp_subject, email, email_verified, first_name, last_name, match_person_id, match_kind)
VALUES ('idp_scan', $1, $1, $2, $3, $4, $5, $6, $7)
ON CONFLICT DO NOTHING`, u.ID, u.Email, u.EmailVerified, u.FirstName, u.LastName, match, kind); err != nil {
			return err
		}
		res.Created++
		return nil
	})
	if err != nil {
		return nil, httperror.New(httperror.CodeUpstream, "could not scan the identity provider: "+err.Error()).WithCause(err)
	}
	return res, nil
}

// RunScans scans every interval until ctx ends.
func (s *ReviewService) RunScans(ctx context.Context, interval time.Duration) {
	if s.scanner == nil || interval <= 0 {
		return
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if res, err := s.Scan(ctx); err != nil && ctx.Err() == nil {
			s.logger.Warn().Err(err).Msg("review scan failed")
		} else if res != nil && res.Created+res.Linked > 0 {
			s.logger.Info().Int("created", res.Created).Int("linked", res.Linked).Msg("review scan")
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// SelfServiceInput is what a volunteer fills in on the "waiting for a
// coordinator" page.
type SelfServiceInput struct {
	FirstName        string                  `json:"first_name"`
	LastName         string                  `json:"last_name"`
	Nickname         string                  `json:"nickname"`
	Phone            string                  `json:"phone"`
	DateOfBirth      string                  `json:"date_of_birth"`
	Address          domain.Address          `json:"address"`
	EmergencyContact domain.EmergencyContact `json:"emergency_contact"`
	SocialHandles    map[string]string       `json:"social_handles,omitempty"`
	DepartmentID     *uuid.UUID              `json:"department_id,omitempty"`
}

// SelfServiceView is the volunteer's own queue entry.
type SelfServiceView struct {
	Status        string            `json:"status"` // pending | ready | accepted | merged | rejected
	Email         string            `json:"email"`
	Item          *SelfServiceInput `json:"item"`
	MissingFields map[string]string `json:"missing_fields"`
}

func (s *ReviewService) selfItem(ctx context.Context, a *actor.Actor) (*ReviewItem, error) {
	it, err := scanReview(s.pool.QueryRow(ctx, `SELECT `+reviewCols+` FROM review_items
 WHERE idp_subject = $1 OR idp_user_id = $1 ORDER BY (status IN ('pending', 'ready')) DESC, created_at DESC LIMIT 1`, a.Subject))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return it, err
}

func selfView(it *ReviewItem) *SelfServiceView {
	in := &SelfServiceInput{FirstName: it.FirstName, LastName: it.LastName, Nickname: it.Nickname, Phone: it.Phone,
		DateOfBirth: it.Data.DateOfBirth, Address: it.Data.Address, EmergencyContact: it.Data.EmergencyContact,
		SocialHandles: it.Data.SocialHandles, DepartmentID: it.DepartmentID}
	p := PersonInput{FirstName: in.FirstName, LastName: in.LastName, Nickname: in.Nickname, Phone: in.Phone, DateOfBirth: in.DateOfBirth,
		Address: in.Address, EmergencyContact: in.EmergencyContact, Email: it.Email, SocialHandles: in.SocialHandles}
	return &SelfServiceView{Status: it.Status, Email: it.Email, Item: in, MissingFields: p.normalize(rules{})}
}

// SelfService returns the caller's own queue entry (nil when none).
func (s *ReviewService) SelfService(ctx context.Context) (*SelfServiceView, error) {
	a, ok := actor.FromContext(ctx)
	if !ok || a.Subject == "" {
		return nil, httperror.Unauthorized("sign in first")
	}
	it, err := s.selfItem(ctx, a)
	if err != nil {
		return nil, httperror.Internal(err)
	}
	if it == nil {
		return nil, httperror.NotFound("registration")
	}
	return selfView(it), nil
}

// SubmitSelfService creates or updates the caller's own queue entry. It
// only ever touches the entry of the signed-in subject, and only while it
// is still open. The entry is "ready" once every required field is filled.
func (s *ReviewService) SubmitSelfService(ctx context.Context, in SelfServiceInput) (*SelfServiceView, error) {
	a, ok := actor.FromContext(ctx)
	if !ok || a.Subject == "" {
		return nil, httperror.Unauthorized("sign in first")
	}
	var person bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM people WHERE idp_subject = $1 OR idp_user_id = $1)`, a.Subject).Scan(&person); err != nil {
		return nil, httperror.Internal(err)
	}
	if person {
		return nil, httperror.Conflict("you already have an HR record; ask a coordinator to change it")
	}
	if in.DepartmentID != nil {
		var ok bool
		if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM departments WHERE id = $1 AND archived_at IS NULL)`, *in.DepartmentID).Scan(&ok); err != nil {
			return nil, httperror.Internal(err)
		}
		if !ok {
			return nil, httperror.Validation("validation failed", map[string]string{"department_id": "unknown department"})
		}
	}
	p := PersonInput{FirstName: in.FirstName, LastName: in.LastName, Nickname: in.Nickname, Phone: in.Phone, DateOfBirth: in.DateOfBirth,
		Address: in.Address, EmergencyContact: in.EmergencyContact, Email: a.Email, SocialHandles: in.SocialHandles}
	issues := p.normalize(rules{})
	for field, msg := range issues {
		if msg == "required" {
			delete(issues, field)
		}
	}
	delete(issues, "email")
	if len(issues) > 0 {
		return nil, httperror.Validation("validation failed", issues)
	}
	status := ReviewPending
	if len(p.normalize(rules{})) == 0 {
		status = ReviewReady
	}
	existing, err := s.selfItem(ctx, a)
	if err != nil {
		return nil, httperror.Internal(err)
	}
	data := ReviewData{DateOfBirth: p.DateOfBirth, Address: p.Address, EmergencyContact: p.EmergencyContact, SocialHandles: p.SocialHandles}
	if existing != nil {
		if existing.Status != ReviewPending && existing.Status != ReviewReady {
			return nil, httperror.Conflict("a coordinator already decided on your registration (" + existing.Status + ")")
		}
		data.Notes, data.Roles, data.Warnings = existing.Data.Notes, existing.Data.Roles, existing.Data.Warnings
	}
	raw, _ := json.Marshal(data)
	match, err := s.matchPerson(ctx, a.Email)
	if err != nil {
		return nil, httperror.Internal(err)
	}
	kind := ""
	if match != nil {
		kind = map[bool]string{true: "verified_email", false: "possible"}[a.EmailVerified]
	}
	if existing == nil {
		_, err = s.pool.Exec(ctx, `
INSERT INTO review_items (source, status, idp_subject, email, email_verified, first_name, last_name, nickname, phone, data, department_id,
                          match_person_id, match_kind)
VALUES ('self_service', $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
			status, a.Subject, a.Email, a.EmailVerified, p.FirstName, p.LastName, p.Nickname, p.Phone, raw, in.DepartmentID, match, kind)
	} else {
		_, err = s.pool.Exec(ctx, `
UPDATE review_items SET status = $2, first_name = $3, last_name = $4, nickname = $5, phone = $6, data = $7,
       department_id = COALESCE($8, department_id), email = CASE WHEN $9 <> '' THEN $9 ELSE email END,
       email_verified = email_verified OR $10, match_person_id = COALESCE(match_person_id, $11),
       match_kind = CASE WHEN match_kind = '' THEN $12 ELSE match_kind END, updated_at = now()
 WHERE id = $1`, existing.ID, status, p.FirstName, p.LastName, p.Nickname, p.Phone, raw, in.DepartmentID, a.Email, a.EmailVerified, match, kind)
	}
	if err != nil {
		return nil, httperror.Internal(err)
	}
	return s.SelfService(ctx)
}

// UpdateInput lets a reviewer assign the department and access groups of
// an open item, or add a note.
// A field left out of the request keeps its value; department_id null
// clears the department.
type ReviewUpdateInput struct {
	DepartmentID *uuid.UUID  `json:"department_id"`
	BundleIDs    []uuid.UUID `json:"bundle_ids"`
	Notes        *string     `json:"notes"`

	hasDepartment, hasBundles bool
}

// UnmarshalJSON records which fields the request carried.
func (in *ReviewUpdateInput) UnmarshalJSON(b []byte) error {
	type plain ReviewUpdateInput
	if err := json.Unmarshal(b, (*plain)(in)); err != nil {
		return err
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(b, &keys); err != nil {
		return err
	}
	_, in.hasDepartment = keys["department_id"]
	_, in.hasBundles = keys["bundle_ids"]
	return nil
}

// Update changes an open item's department, access groups or note.
func (s *ReviewService) Update(ctx context.Context, actor string, id uuid.UUID, in ReviewUpdateInput) (*ReviewItem, error) {
	it, err := s.open(ctx, id)
	if err != nil {
		return nil, err
	}
	if in.Notes != nil {
		it.Data.Notes = strings.TrimSpace(*in.Notes)
	}
	if !in.hasDepartment {
		in.DepartmentID = it.DepartmentID
	}
	if !in.hasBundles {
		in.BundleIDs = it.BundleIDs
	}
	raw, _ := json.Marshal(it.Data)
	bundles, _ := json.Marshal(uniqueIDs(in.BundleIDs))
	if _, err := s.pool.Exec(ctx, `UPDATE review_items SET department_id = $2, bundle_ids = $3, data = $4, updated_at = now() WHERE id = $1`,
		id, in.DepartmentID, bundles, raw); err != nil {
		return nil, httperror.Internal(err)
	}
	return s.Get(ctx, id)
}

// AcceptInput is the reviewer's checked and completed record.
type AcceptInput struct {
	Person       PersonInput     `json:"person"`
	DepartmentID uuid.UUID       `json:"department_id"`
	Roles        []RolePlanInput `json:"roles,omitempty"`
	BundleIDs    []uuid.UUID     `json:"bundle_ids,omitempty"`
	// Complete finishes the onboarding right away (queues provisioning:
	// hrPersonId and groups on the account).
	Complete bool   `json:"complete,omitempty"`
	Note     string `json:"note,omitempty"`
}

// AcceptResult is the new person's onboarding.
type AcceptResult struct {
	Item       *ReviewItem     `json:"item"`
	Onboarding *OnboardingView `json:"onboarding"`
	Complete   *CompleteResult `json:"complete,omitempty"`
}

// Accept creates the person and an onboarding in the department, linked to
// the item's account.
func (s *ReviewService) Accept(ctx context.Context, actorName string, id uuid.UUID, in AcceptInput) (*AcceptResult, error) {
	it, err := s.open(ctx, id)
	if err != nil {
		return nil, err
	}
	if in.Person.Email == "" {
		in.Person.Email = it.Email
	}
	view, err := s.onboarding.Start(ctx, actorName, OnboardingDetailsInput{Person: in.Person, DepartmentID: in.DepartmentID})
	if err != nil {
		return nil, err
	}
	if it.IdPUserID != "" {
		var subject string
		_ = s.pool.QueryRow(ctx, `SELECT COALESCE(idp_subject, '') FROM review_items WHERE id = $1`, id).Scan(&subject)
		if err := linkAccount(ctx, s.pool, view.Person.ID, it.IdPUserID, subject); err != nil {
			return nil, err
		}
	} else if it.Source == ReviewSourceSelf {
		var subject string
		_ = s.pool.QueryRow(ctx, `SELECT COALESCE(idp_subject, '') FROM review_items WHERE id = $1`, id).Scan(&subject)
		if subject != "" {
			if _, err := s.pool.Exec(ctx, `UPDATE people SET idp_subject = $2 WHERE id = $1`, view.Person.ID, subject); err != nil {
				return nil, httperror.Internal(err)
			}
		}
	}
	bundles := in.BundleIDs
	if bundles == nil {
		bundles = it.BundleIDs
	}
	if len(in.Roles) > 0 || len(bundles) > 0 {
		cur, err := s.drafts.Get(ctx, view.Draft.ID)
		if err != nil {
			return nil, err
		}
		roles := append(individualRoles(cur.Roles), in.Roles...)
		if _, err := s.drafts.AssignRoles(ctx, actorName, view.Draft.ID, AssignRolesInput{Roles: roles, BundleIDs: append(cur.BundleIDs, bundles...)}); err != nil {
			return nil, err
		}
	}
	if _, err := s.pool.Exec(ctx, `
UPDATE review_items SET status = 'accepted', person_id = $2, draft_id = $3, decided_by = $4, decided_at = now(), decision_note = $5,
       updated_at = now() WHERE id = $1`, id, view.Person.ID, view.Draft.ID, actorName, strings.TrimSpace(in.Note)); err != nil {
		return nil, httperror.Internal(err)
	}
	if err := s.audit.Record(ctx, actorName, "review.accept", "person", view.Person.ID, map[string]any{
		"review_item": id, "source": it.Source, "linked_account": it.IdPUserID != "",
	}); err != nil {
		return nil, err
	}
	out := &AcceptResult{}
	if in.Complete {
		if out.Complete, err = s.prov.Complete(ctx, actorName, view.Draft.ID); err != nil {
			return nil, err
		}
	}
	if out.Onboarding, err = s.onboarding.View(ctx, view.Draft.ID); err != nil {
		return nil, err
	}
	out.Item, err = s.Get(ctx, id)
	return out, err
}

// Merge links the item (and its account) to an existing person.
func (s *ReviewService) Merge(ctx context.Context, actorName string, id, personID uuid.UUID, note string) (*ReviewItem, error) {
	it, err := s.open(ctx, id)
	if err != nil {
		return nil, err
	}
	p, err := s.people.Get(ctx, personID)
	if err != nil {
		return nil, err
	}
	warnings := []string{}
	if it.IdPUserID != "" {
		if p.IdPUserID != "" && p.IdPUserID != it.IdPUserID {
			return nil, httperror.Conflict(p.DisplayName + " already has another account; offboard or fix that first")
		}
		var subject string
		_ = s.pool.QueryRow(ctx, `SELECT COALESCE(idp_subject, '') FROM review_items WHERE id = $1`, id).Scan(&subject)
		if warnings, err = s.lifecycle.LinkAccount(ctx, actorName, personID, it.IdPUserID, subject); err != nil {
			return nil, err
		}
	}
	if _, err := s.pool.Exec(ctx, `
UPDATE review_items SET status = 'merged', person_id = $2, decided_by = $3, decided_at = now(), decision_note = $4, updated_at = now()
 WHERE id = $1`, id, personID, actorName, strings.TrimSpace(note)); err != nil {
		return nil, httperror.Internal(err)
	}
	if err := s.audit.Record(ctx, actorName, "review.merge", "person", personID, map[string]any{
		"review_item": id, "source": it.Source, "linked_account": it.IdPUserID != "", "warnings": warnings,
	}); err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

// Reject closes the item and disables its account in HR's realm (not in the
// convention's SSO; link mode leaves the account alone).
func (s *ReviewService) Reject(ctx context.Context, actorName string, id uuid.UUID, reason string) (*ReviewItem, []string, error) {
	it, err := s.open(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	warnings := []string{}
	if it.IdPUserID != "" && s.accounts != nil {
		if s.linkOnly {
			warnings = append(warnings, "The account belongs to the convention's identity provider and was not disabled.")
		} else if err := s.accounts.SetEnabled(ctx, it.IdPUserID, false); err != nil && !errors.Is(err, provisioners.ErrAccountNotFound) {
			return nil, nil, idpFailure("disable the account", err)
		}
	}
	if _, err := s.pool.Exec(ctx, `
UPDATE review_items SET status = 'rejected', decided_by = $2, decided_at = now(), decision_note = $3, updated_at = now() WHERE id = $1`,
		id, actorName, strings.TrimSpace(reason)); err != nil {
		return nil, nil, httperror.Internal(err)
	}
	if err := s.audit.Record(ctx, actorName, "review.reject", "review_item", id, map[string]any{
		"source": it.Source, "account_disabled": it.IdPUserID != "" && !s.linkOnly, "reason": strings.TrimSpace(reason),
	}); err != nil {
		return nil, nil, err
	}
	it, err = s.Get(ctx, id)
	return it, warnings, err
}
