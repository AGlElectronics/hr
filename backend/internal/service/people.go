package service

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AGlElectronics/hr/backend/internal/audit"
	"github.com/AGlElectronics/hr/backend/internal/domain"
	"github.com/AGlElectronics/hr/backend/internal/httperror"
	"github.com/AGlElectronics/hr/backend/internal/provisioners"
)

// PeopleService owns CRUD over the `people` table. Draft operations live
// in DraftService so each entity has one clear owner.
type PeopleService struct {
	pool      *pgxpool.Pool
	audit     *audit.Writer
	settings  *SettingsService
	usernames provisioners.UsernameDirectory // nil when the IdP cannot be asked
}

// NewPeopleService wires a PeopleService to its collaborators. usernames may
// be nil (login names are then only checked against HR's own records).
func NewPeopleService(pool *pgxpool.Pool, aud *audit.Writer, settings *SettingsService, usernames provisioners.UsernameDirectory) *PeopleService {
	return &PeopleService{pool: pool, audit: aud, settings: settings, usernames: usernames}
}

// PersonInput is the writeable surface of a person (create and full update).
type PersonInput struct {
	FirstName        string                  `json:"first_name"`
	LastName         string                  `json:"last_name"`
	Nickname         string                  `json:"nickname,omitempty"`
	DateOfBirth      string                  `json:"date_of_birth,omitempty"`
	IsBanned         bool                    `json:"is_banned,omitempty"`
	Email            string                  `json:"email"`
	OrgEmail         string                  `json:"org_email,omitempty"`
	Phone            string                  `json:"phone,omitempty"`
	Address          domain.Address          `json:"address"`
	SocialHandles    map[string]string       `json:"social_handles,omitempty"`
	EmergencyContact domain.EmergencyContact `json:"emergency_contact"`
	Status           string                  `json:"status,omitempty"`
	Notes            string                  `json:"notes,omitempty"`
}

// CreatePersonInput is kept as an alias for handler readability.
type CreatePersonInput = PersonInput

var knownSocials = map[string]bool{"telegram": true, "discord": true, "x": true, "instagram": true, "bluesky": true, "mastodon": true}

// rules are the settings validation depends on.
type rules struct {
	defaultCountry string
	orgDomain      string
}

func (s *PeopleService) rules(ctx context.Context) (rules, *Settings, error) {
	if s.settings == nil {
		return rules{}, &Settings{UsernameTemplate: DefaultUsernameTemplate, OrgEmailTemplate: DefaultOrgEmailTemplate}, nil
	}
	st, err := s.settings.Get(ctx)
	if err != nil {
		return rules{}, nil, err
	}
	return rules{defaultCountry: st.DefaultCountry, orgDomain: st.OrgEmailDomain}, st, nil
}

// normalize cleans input and returns field-level validation issues. Phone
// numbers are rewritten to E.164 when valid.
func (in *PersonInput) normalize(r rules) map[string]string {
	t := strings.TrimSpace
	in.FirstName, in.LastName, in.Nickname = cleanName(in.FirstName), cleanName(in.LastName), cleanName(in.Nickname)
	in.Email, in.OrgEmail = strings.ToLower(t(in.Email)), strings.ToLower(t(in.OrgEmail))
	in.Phone, in.Notes, in.Status = t(in.Phone), t(in.Notes), t(in.Status)
	in.Address = normalizeAddress(in.Address)
	ec := &in.EmergencyContact
	ec.Name, ec.Phone, ec.Email, ec.Comments = cleanName(ec.Name), t(ec.Phone), strings.ToLower(t(ec.Email)), t(ec.Comments)
	in.DateOfBirth = t(in.DateOfBirth)

	// Every field of the volunteer definition is required except the
	// organization email, social handles, and the emergency contact's
	// email/comments.
	issues := map[string]string{}
	for field, val := range map[string]string{
		"first_name": in.FirstName, "last_name": in.LastName, "nickname": in.Nickname,
		"date_of_birth": in.DateOfBirth, "phone": in.Phone,
		"emergency_contact.name": ec.Name, "emergency_contact.phone": ec.Phone,
	} {
		if val == "" {
			issues[field] = "required"
		}
	}
	for field, val := range map[string]string{"first_name": in.FirstName, "last_name": in.LastName, "nickname": in.Nickname, "emergency_contact.name": ec.Name} {
		if val != "" && !validName(val) {
			issues[field] = nameRule
		}
	}
	addressIssues(in.Address, issues)
	if in.DateOfBirth != "" {
		dob, err := time.Parse("2006-01-02", in.DateOfBirth)
		switch {
		case err != nil:
			issues["date_of_birth"] = "must be a date (YYYY-MM-DD)"
		case dob.After(time.Now()) || dob.Before(time.Now().AddDate(-120, 0, 0)):
			issues["date_of_birth"] = "must be a realistic date of birth"
		}
	}
	switch {
	case in.Email == "":
		issues["email"] = "required"
	case !validEmail(in.Email):
		issues["email"] = "must be a valid email address"
	}
	if in.OrgEmail != "" {
		if msg := orgEmailIssue(in.OrgEmail, r.orgDomain); msg != "" {
			issues["org_email"] = msg
		}
	}
	if in.Phone != "" {
		if e164 := NormalizePhone(in.Phone, r.defaultCountry); e164 != "" {
			in.Phone = e164
		} else {
			issues["phone"] = phoneRule(r.defaultCountry)
		}
	}
	if ec.Phone != "" {
		if e164 := NormalizePhone(ec.Phone, r.defaultCountry); e164 != "" {
			ec.Phone = e164
		} else {
			issues["emergency_contact.phone"] = phoneRule(r.defaultCountry)
		}
	}
	if ec.Email != "" && !validEmail(ec.Email) {
		issues["emergency_contact.email"] = "must be a valid email address"
	}
	if in.Status == "" {
		in.Status = string(domain.PersonApplicant)
	} else if !domain.PersonStatus(in.Status).Valid() {
		issues["status"] = "must be one of applicant, onboarding, active, paused"
	}
	socials := map[string]string{}
	for k, v := range in.SocialHandles {
		k, v = strings.ToLower(t(k)), t(v)
		if v == "" {
			continue
		}
		if !knownSocials[k] {
			issues["social_handles."+k] = "unknown network"
			continue
		}
		socials[k] = v
	}
	in.SocialHandles = socials
	return issues
}

func (in *PersonInput) names() (legal, display string) {
	legal = strings.TrimSpace(in.FirstName + " " + in.LastName)
	display = legal
	if in.Nickname != "" {
		display = in.Nickname
	}
	return
}

const personCols = `id, first_name, last_name, COALESCE(nickname, ''), COALESCE(username, ''),
COALESCE(to_char(date_of_birth, 'YYYY-MM-DD'), ''), is_banned, legal_name, display_name, email,
COALESCE(org_email, ''), COALESCE(phone, ''), address, social_handles, emergency_contact,
status, COALESCE(notes, ''), COALESCE(idp_user_id, ''), COALESCE(idp_subject, ''), paused_at, created_at, updated_at`

// personInputFrom rebuilds the writeable surface of a stored person so the
// same validation can check whether a profile is complete.
func personInputFrom(p *domain.Person) PersonInput {
	return PersonInput{
		FirstName: p.FirstName, LastName: p.LastName, Nickname: p.Nickname, DateOfBirth: p.DateOfBirth,
		IsBanned: p.IsBanned, Email: p.Email, OrgEmail: p.OrgEmail, Phone: p.Phone, Address: p.Address,
		SocialHandles: p.SocialHandles, EmergencyContact: p.EmergencyContact, Status: string(p.Status), Notes: p.Notes,
	}
}

// MissingFields returns the validation issues that keep a stored person from
// being onboarded (empty when the profile is complete). Stored phone numbers
// are already E.164, so no default country is needed.
func MissingFields(p *domain.Person) map[string]string {
	in := personInputFrom(p)
	return in.normalize(rules{})
}

type rowScanner interface{ Scan(dest ...any) error }

func scanPerson(row rowScanner) (*domain.Person, error) {
	p := &domain.Person{}
	var address, socials, emergency []byte
	if err := row.Scan(&p.ID, &p.FirstName, &p.LastName, &p.Nickname, &p.Username, &p.DateOfBirth, &p.IsBanned, &p.LegalName, &p.DisplayName, &p.Email,
		&p.OrgEmail, &p.Phone, &address, &socials, &emergency, &p.Status, &p.Notes, &p.IdPUserID, &p.IdPSubject, &p.PausedAt,
		&p.CreatedAt, &p.UpdatedAt); err != nil {
		return nil, err
	}
	p.SocialHandles = map[string]string{}
	_ = json.Unmarshal(address, &p.Address)
	_ = json.Unmarshal(socials, &p.SocialHandles)
	_ = json.Unmarshal(emergency, &p.EmergencyContact)
	return p, nil
}

// checkUnique refuses an email, login name or organization email that
// someone else already uses (in HR, or for the login name in the IdP).
func (s *PeopleService) checkUnique(ctx context.Context, self uuid.UUID, email, username, orgEmail string) error {
	issues := map[string]string{}
	var field string
	err := s.pool.QueryRow(ctx, `
SELECT CASE WHEN lower(email) = $2 THEN 'email'
            WHEN $3 <> '' AND lower(COALESCE(username, '')) = $3 THEN 'username'
            ELSE 'org_email' END
  FROM people
 WHERE id <> $1 AND (lower(email) = $2 OR ($3 <> '' AND lower(COALESCE(username, '')) = $3)
                     OR ($4 <> '' AND lower(COALESCE(org_email, '')) = $4))
 LIMIT 1`, self, email, strings.ToLower(username), orgEmail).Scan(&field)
	switch {
	case err == nil:
		switch field {
		case "email":
			issues["email"] = "another person already uses this email"
		case "username":
			issues["nickname"] = "this nickname is already taken (login name " + username + ")"
		default:
			issues["org_email"] = "another person already uses this organization email"
		}
	case !errors.Is(err, pgx.ErrNoRows):
		return httperror.Internal(err)
	}
	if issues["nickname"] == "" && username != "" && s.usernames != nil {
		owner, found, err := s.usernames.UsernameOwner(ctx, username)
		if err == nil && found && owner != self.String() {
			issues["nickname"] = "this nickname is already taken (login name " + username + ")"
		}
	}
	if len(issues) > 0 {
		return httperror.Validation("validation failed", issues)
	}
	return nil
}

// UsernameCheck is the login name a nickname would give and whether it is free.
type UsernameCheck struct {
	Username  string `json:"username"`
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

// CheckUsername previews the login name for the given names (self is the
// person being edited, or uuid.Nil for a new one).
func (s *PeopleService) CheckUsername(ctx context.Context, self uuid.UUID, first, last, nickname string) (*UsernameCheck, error) {
	_, st, err := s.rules(ctx)
	if err != nil {
		return nil, err
	}
	first, last, nickname = cleanName(first), cleanName(last), cleanName(nickname)
	for _, n := range []string{first, last, nickname} {
		if n != "" && !validName(n) {
			return &UsernameCheck{Reason: "names " + nameRule}, nil
		}
	}
	u := UsernameFor(st.UsernameTemplate, &domain.Person{FirstName: first, LastName: last, Nickname: nickname})
	out := &UsernameCheck{Username: u}
	if len(u) < 3 {
		out.Reason = "must give a login name of at least 3 letters"
		return out, nil
	}
	err = s.checkUnique(ctx, self, "", u, "")
	var he *httperror.Error
	switch {
	case err == nil:
		out.Available = true
	case errors.As(err, &he) && he.Code == httperror.CodeValidation:
		out.Reason = "this nickname is already taken"
	default:
		return nil, err
	}
	return out, nil
}

// prepare validates the input and works out the login name.
func (s *PeopleService) prepare(ctx context.Context, self uuid.UUID, in *PersonInput) (string, error) {
	r, st, err := s.rules(ctx)
	if err != nil {
		return "", err
	}
	issues := in.normalize(r)
	username := ""
	if in.Nickname != "" || in.FirstName != "" {
		username = UsernameFor(st.UsernameTemplate, &domain.Person{FirstName: in.FirstName, LastName: in.LastName, Nickname: in.Nickname})
		if len(username) < 3 && issues["nickname"] == "" {
			issues["nickname"] = "must give a login name of at least 3 letters"
		}
	}
	if len(issues) > 0 {
		return "", httperror.Validation("validation failed", issues)
	}
	if err := s.checkUnique(ctx, self, in.Email, username, in.OrgEmail); err != nil {
		return "", err
	}
	return username, nil
}

// Create inserts a new person and emits an audit event.
func (s *PeopleService) Create(ctx context.Context, actor string, in PersonInput) (*domain.Person, error) {
	username, err := s.prepare(ctx, uuid.Nil, &in)
	if err != nil {
		return nil, err
	}
	legal, display := in.names()
	address, _ := json.Marshal(in.Address)
	socials, _ := json.Marshal(in.SocialHandles)
	emergency, _ := json.Marshal(in.EmergencyContact)
	now := time.Now().UTC()

	p, err := scanPerson(s.pool.QueryRow(ctx, `
INSERT INTO people (id, first_name, last_name, nickname, legal_name, display_name, email, org_email,
                    phone, address, social_handles, emergency_contact, status, notes, created_at, updated_at,
                    date_of_birth, is_banned, username)
VALUES ($1, $2, $3, NULLIF($4, ''), $5, $6, $7, NULLIF($8, ''), NULLIF($9, ''), $10,
        $11, $12, $13, NULLIF($14, ''), $15, $15, NULLIF($16, '')::date, $17, NULLIF($18, ''))
RETURNING `+personCols,
		uuid.New(), in.FirstName, in.LastName, in.Nickname, legal, display, in.Email, in.OrgEmail,
		in.Phone, address, socials, emergency, in.Status, in.Notes, now, in.DateOfBirth, in.IsBanned, username))
	if err != nil {
		return nil, httperror.Internal(err)
	}
	if err := s.audit.Record(ctx, actor, "person.create", "person", p.ID, map[string]any{
		"username": p.Username,
	}); err != nil {
		return nil, err
	}
	return p, nil
}

// Update replaces every writeable field of a person.
func (s *PeopleService) Update(ctx context.Context, actor string, id uuid.UUID, in PersonInput) (*domain.Person, error) {
	prev, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	username, err := s.prepare(ctx, id, &in)
	if err != nil {
		return nil, err
	}
	legal, display := in.names()
	address, _ := json.Marshal(in.Address)
	socials, _ := json.Marshal(in.SocialHandles)
	emergency, _ := json.Marshal(in.EmergencyContact)

	p, err := scanPerson(s.pool.QueryRow(ctx, `
UPDATE people SET first_name = $2, last_name = $3, nickname = NULLIF($4, ''), legal_name = $5,
       display_name = $6, email = $7, org_email = NULLIF($8, ''), phone = NULLIF($9, ''),
       address = $10, social_handles = $11, emergency_contact = $12, status = $13,
       notes = NULLIF($14, ''), date_of_birth = NULLIF($15, '')::date, is_banned = $16,
       username = NULLIF($17, ''), updated_at = now()
 WHERE id = $1
RETURNING `+personCols,
		id, in.FirstName, in.LastName, in.Nickname, legal, display, in.Email, in.OrgEmail,
		in.Phone, address, socials, emergency, in.Status, in.Notes, in.DateOfBirth, in.IsBanned, username))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, httperror.NotFound("person")
		}
		return nil, httperror.Internal(err)
	}
	if changes := PersonDiff(prev, p); len(changes) > 0 {
		if err := s.audit.Record(ctx, actor, "person.update", "person", p.ID, map[string]any{
			"username": p.Username, "changes": changes,
		}); err != nil {
			return nil, err
		}
	}
	return p, nil
}

// sensitiveFields are recorded in the audit log as changed, without their
// values, so the long-lived log does not copy personal data.
var sensitiveFields = map[string]bool{
	"date_of_birth": true, "phone": true, "address": true, "emergency_contact": true, "social_handles": true, "notes": true,
}

// PersonDiff lists the fields that differ between two versions of a person:
// {"field": {"from": old, "to": new}}, or {"changed": true} for sensitive
// fields.
func PersonDiff(prev, next *domain.Person) map[string]any {
	type pair struct{ from, to any }
	fields := map[string]pair{
		"first_name":        {prev.FirstName, next.FirstName},
		"last_name":         {prev.LastName, next.LastName},
		"nickname":          {prev.Nickname, next.Nickname},
		"username":          {prev.Username, next.Username},
		"email":             {prev.Email, next.Email},
		"org_email":         {prev.OrgEmail, next.OrgEmail},
		"status":            {prev.Status, next.Status},
		"is_banned":         {prev.IsBanned, next.IsBanned},
		"date_of_birth":     {prev.DateOfBirth, next.DateOfBirth},
		"phone":             {prev.Phone, next.Phone},
		"address":           {prev.Address, next.Address},
		"emergency_contact": {prev.EmergencyContact, next.EmergencyContact},
		"social_handles":    {prev.SocialHandles, next.SocialHandles},
		"notes":             {prev.Notes, next.Notes},
	}
	out := map[string]any{}
	for name, f := range fields {
		a, _ := json.Marshal(f.from)
		b, _ := json.Marshal(f.to)
		if string(a) == string(b) {
			continue
		}
		if sensitiveFields[name] {
			out[name] = map[string]bool{"changed": true}
		} else {
			out[name] = map[string]any{"from": f.from, "to": f.to}
		}
	}
	return out
}

// CriticalChanges lists the profile-critical fields that differ: the ones a
// person may never change on their own record (names and login name, email
// addresses, ban, status, notes).
func CriticalChanges(prev *domain.Person, in PersonInput) []string {
	var out []string
	t := strings.TrimSpace
	for name, changed := range map[string]bool{
		"first_name": cleanName(in.FirstName) != prev.FirstName,
		"last_name":  cleanName(in.LastName) != prev.LastName,
		"nickname":   cleanName(in.Nickname) != prev.Nickname,
		"email":      !strings.EqualFold(t(in.Email), prev.Email),
		"org_email":  !strings.EqualFold(t(in.OrgEmail), prev.OrgEmail),
		"is_banned":  in.IsBanned != prev.IsBanned,
		"notes":      t(in.Notes) != prev.Notes,
	} {
		if changed {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// SetStatus moves a person's lifecycle status (used by draft transitions).
func (s *PeopleService) SetStatus(ctx context.Context, id uuid.UUID, status domain.PersonStatus) error {
	if _, err := s.pool.Exec(ctx, `
UPDATE people SET status = $2, paused_at = CASE WHEN $2 = 'paused' THEN COALESCE(paused_at, now()) END, updated_at = now()
 WHERE id = $1`, id, string(status)); err != nil {
		return httperror.Internal(err)
	}
	return nil
}

// Get returns a person by id, or ErrNotFound.
func (s *PeopleService) Get(ctx context.Context, id uuid.UUID) (*domain.Person, error) {
	p, err := scanPerson(s.pool.QueryRow(ctx, `SELECT `+personCols+` FROM people WHERE id = $1`, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, httperror.NotFound("person")
		}
		return nil, httperror.Internal(err)
	}
	return p, nil
}

// ListPeopleInput filters and pages the people list.
type ListPeopleInput struct {
	// Query matches name, nickname, login name and email addresses.
	Query  string
	Status string
	// DepartmentIDs, when not nil, limits the list to people with an
	// onboarding or membership in these departments (department leads).
	DepartmentIDs []uuid.UUID
	// DepartmentID filters to one department (uuid.Nil: all).
	DepartmentID uuid.UUID
	Limit        int
	Offset       int
}

// Page is one page of a list.
type Page[T any] struct {
	Items  []T `json:"items"`
	Total  int `json:"total"`
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

// MaxPageSize caps limit on every paged list.
const MaxPageSize = 200

// NormalizePaging clamps limit (default 50) and offset.
func NormalizePaging(limit, offset int) (int, int) {
	if limit <= 0 {
		limit = 50
	}
	if limit > MaxPageSize {
		limit = MaxPageSize
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

// List returns one page of people (reduced fields) ordered by display name.
func (s *PeopleService) List(ctx context.Context, in ListPeopleInput) (*Page[domain.PersonSummary], error) {
	in.Limit, in.Offset = NormalizePaging(in.Limit, in.Offset)
	q := "%" + strings.ToLower(strings.TrimSpace(in.Query)) + "%"
	scoped := in.DepartmentIDs != nil
	if in.DepartmentIDs == nil {
		in.DepartmentIDs = []uuid.UUID{}
	}
	rows, err := s.pool.Query(ctx, `
SELECT id, display_name, legal_name, COALESCE(nickname, ''), COALESCE(username, ''), email, COALESCE(org_email, ''),
       status, is_banned, updated_at, count(*) OVER ()
  FROM people
 WHERE ($1 = '%%' OR lower(legal_name) LIKE $1 OR lower(display_name) LIKE $1 OR lower(email) LIKE $1
        OR lower(COALESCE(nickname, '')) LIKE $1 OR lower(COALESCE(username, '')) LIKE $1 OR lower(COALESCE(org_email, '')) LIKE $1)
   AND ($2 = '' OR status = $2)
   AND (NOT $3 OR EXISTS (SELECT 1 FROM employment_drafts d WHERE d.person_id = people.id
                           AND d.department_id = ANY($4) AND d.state <> 'rejected'))
   AND ($5 = '00000000-0000-0000-0000-000000000000'::uuid OR EXISTS (SELECT 1 FROM employment_drafts d
                           WHERE d.person_id = people.id AND d.department_id = $5 AND d.state <> 'rejected'))
 ORDER BY lower(display_name), id
 LIMIT $6 OFFSET $7`, q, strings.TrimSpace(in.Status), scoped, in.DepartmentIDs, in.DepartmentID, in.Limit, in.Offset)
	if err != nil {
		return nil, httperror.Internal(err)
	}
	defer rows.Close()
	out := &Page[domain.PersonSummary]{Items: []domain.PersonSummary{}, Limit: in.Limit, Offset: in.Offset}
	for rows.Next() {
		var p domain.PersonSummary
		if err := rows.Scan(&p.ID, &p.DisplayName, &p.LegalName, &p.Nickname, &p.Username, &p.Email, &p.OrgEmail,
			&p.Status, &p.IsBanned, &p.UpdatedAt, &out.Total); err != nil {
			return nil, httperror.Internal(err)
		}
		out.Items = append(out.Items, p)
	}
	if err := rows.Err(); err != nil {
		return nil, httperror.Internal(err)
	}
	if len(out.Items) == 0 && in.Offset > 0 {
		if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM people`).Scan(&out.Total); err != nil {
			return nil, httperror.Internal(err)
		}
	}
	return out, nil
}

// All returns every person with every field (internal use: dashboard,
// retention).
func (s *PeopleService) All(ctx context.Context) ([]domain.Person, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+personCols+` FROM people ORDER BY lower(display_name)`)
	if err != nil {
		return nil, httperror.Internal(err)
	}
	defer rows.Close()
	out := make([]domain.Person, 0, 16)
	for rows.Next() {
		p, err := scanPerson(rows)
		if err != nil {
			return nil, httperror.Internal(err)
		}
		out = append(out, *p)
	}
	if err := rows.Err(); err != nil {
		return nil, httperror.Internal(err)
	}
	return out, nil
}
