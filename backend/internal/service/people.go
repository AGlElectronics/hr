package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AGlElectronics/hr/backend/internal/audit"
	"github.com/AGlElectronics/hr/backend/internal/domain"
	"github.com/AGlElectronics/hr/backend/internal/httperror"
)

// PeopleService owns CRUD over the `people` table. Draft operations live
// in DraftService so each entity has one clear owner.
type PeopleService struct {
	pool  *pgxpool.Pool
	audit *audit.Writer
}

// NewPeopleService wires a PeopleService to its collaborators.
func NewPeopleService(pool *pgxpool.Pool, aud *audit.Writer) *PeopleService {
	return &PeopleService{pool: pool, audit: aud}
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
	Address          string                  `json:"address,omitempty"`
	SocialHandles    map[string]string       `json:"social_handles,omitempty"`
	EmergencyContact domain.EmergencyContact `json:"emergency_contact"`
	Status           string                  `json:"status,omitempty"`
	Notes            string                  `json:"notes,omitempty"`
}

// CreatePersonInput is kept as an alias for handler readability.
type CreatePersonInput = PersonInput

var phoneRE = regexp.MustCompile(`^\+?[0-9 ()\-./]{5,24}$`)

var knownSocials = map[string]bool{"telegram": true, "discord": true, "x": true, "instagram": true, "bluesky": true, "mastodon": true}

func validEmail(s string) bool {
	a, err := mail.ParseAddress(s)
	return err == nil && a.Address == s && strings.Contains(s[strings.LastIndex(s, "@"):], ".")
}

// normalize trims input and returns field-level validation issues.
func (in *PersonInput) normalize() map[string]string {
	t := strings.TrimSpace
	in.FirstName, in.LastName, in.Nickname = t(in.FirstName), t(in.LastName), t(in.Nickname)
	in.Email, in.OrgEmail = strings.ToLower(t(in.Email)), strings.ToLower(t(in.OrgEmail))
	in.Phone, in.Address, in.Notes, in.Status = t(in.Phone), t(in.Address), t(in.Notes), t(in.Status)
	ec := &in.EmergencyContact
	ec.Name, ec.Phone, ec.Email, ec.Comments = t(ec.Name), t(ec.Phone), strings.ToLower(t(ec.Email)), t(ec.Comments)

	in.DateOfBirth = t(in.DateOfBirth)

	// Every field of the volunteer definition is required except the
	// organization email (assigned by workspace provisioning), social
	// handles, and the emergency contact's email/comments.
	issues := map[string]string{}
	for field, val := range map[string]string{
		"first_name": in.FirstName, "last_name": in.LastName, "nickname": in.Nickname,
		"date_of_birth": in.DateOfBirth, "address": in.Address, "phone": in.Phone,
		"emergency_contact.name": ec.Name, "emergency_contact.phone": ec.Phone,
	} {
		if val == "" {
			issues[field] = "required"
		}
	}
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
	case !validEmail(in.Email) || strings.HasSuffix(in.Email, domain.PlaceholderEmailDomain):
		issues["email"] = "must be a valid email address"
	}
	if in.OrgEmail != "" && !validEmail(in.OrgEmail) {
		issues["org_email"] = "must be a valid email address"
	}
	if in.Phone != "" && !phoneRE.MatchString(in.Phone) {
		issues["phone"] = "must look like a phone number, e.g. +43 660 1234567"
	}
	if ec.Phone != "" && !phoneRE.MatchString(ec.Phone) {
		issues["emergency_contact.phone"] = "must look like a phone number"
	}
	if ec.Email != "" && !validEmail(ec.Email) {
		issues["emergency_contact.email"] = "must be a valid email address"
	}
	if in.Status == "" {
		in.Status = string(domain.PersonApplicant)
	} else if !domain.PersonStatus(in.Status).Valid() {
		issues["status"] = "must be one of applicant, onboarding, active, paused, offboarded"
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

const personCols = `id, first_name, last_name, COALESCE(nickname, ''),
COALESCE(to_char(date_of_birth, 'YYYY-MM-DD'), ''), is_banned, legal_name, display_name, email,
COALESCE(org_email, ''), COALESCE(phone, ''), COALESCE(address, ''), social_handles, emergency_contact,
status, COALESCE(notes, ''), offboarded_at, COALESCE(offboard_reason, ''), created_at, updated_at`

// personInputFrom rebuilds the writeable surface of a stored person so the
// same validation can check whether a profile is complete.
func personInputFrom(p *domain.Person) PersonInput {
	email := p.Email
	if p.EmailMissing {
		email = ""
	}
	return PersonInput{
		FirstName: p.FirstName, LastName: p.LastName, Nickname: p.Nickname, DateOfBirth: p.DateOfBirth,
		IsBanned: p.IsBanned, Email: email, OrgEmail: p.OrgEmail, Phone: p.Phone, Address: p.Address,
		SocialHandles: p.SocialHandles, EmergencyContact: p.EmergencyContact, Status: string(p.Status), Notes: p.Notes,
	}
}

// MissingFields returns the validation issues that keep a stored person from
// being onboarded (empty when the profile is complete).
func MissingFields(p *domain.Person) map[string]string {
	in := personInputFrom(p)
	return in.normalize()
}

type rowScanner interface{ Scan(dest ...any) error }

func scanPerson(row rowScanner) (*domain.Person, error) {
	p := &domain.Person{}
	var socials, emergency []byte
	if err := row.Scan(&p.ID, &p.FirstName, &p.LastName, &p.Nickname, &p.DateOfBirth, &p.IsBanned, &p.LegalName, &p.DisplayName, &p.Email,
		&p.OrgEmail, &p.Phone, &p.Address, &socials, &emergency, &p.Status, &p.Notes, &p.OffboardedAt, &p.OffboardReason, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return nil, err
	}
	p.SocialHandles = map[string]string{}
	_ = json.Unmarshal(socials, &p.SocialHandles)
	_ = json.Unmarshal(emergency, &p.EmergencyContact)
	p.EmailMissing = strings.HasSuffix(p.Email, domain.PlaceholderEmailDomain)
	return p, nil
}

func (s *PeopleService) ensureEmailFree(ctx context.Context, email string, except uuid.UUID) error {
	var other uuid.UUID
	err := s.pool.QueryRow(ctx, `SELECT id FROM people WHERE lower(email) = $1 AND id <> $2 LIMIT 1`, email, except).Scan(&other)
	if err == nil {
		return httperror.Validation("validation failed", map[string]string{"email": "another person already uses this email"})
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return httperror.Internal(err)
	}
	return nil
}

// Create inserts a new person and emits an audit event.
func (s *PeopleService) Create(ctx context.Context, actor string, in PersonInput) (*domain.Person, error) {
	if issues := in.normalize(); len(issues) > 0 {
		return nil, httperror.Validation("validation failed", issues)
	}
	if err := s.ensureEmailFree(ctx, in.Email, uuid.Nil); err != nil {
		return nil, err
	}
	legal, display := in.names()
	socials, _ := json.Marshal(in.SocialHandles)
	emergency, _ := json.Marshal(in.EmergencyContact)
	now := time.Now().UTC()

	p, err := scanPerson(s.pool.QueryRow(ctx, `
INSERT INTO people (id, first_name, last_name, nickname, legal_name, display_name, email, org_email,
                    phone, address, social_handles, emergency_contact, status, notes, created_at, updated_at,
                    date_of_birth, is_banned)
VALUES ($1, $2, $3, NULLIF($4, ''), $5, $6, $7, NULLIF($8, ''), NULLIF($9, ''), NULLIF($10, ''),
        $11, $12, $13, NULLIF($14, ''), $15, $15, NULLIF($16, '')::date, $17)
RETURNING `+personCols,
		uuid.New(), in.FirstName, in.LastName, in.Nickname, legal, display, in.Email, in.OrgEmail,
		in.Phone, in.Address, socials, emergency, in.Status, in.Notes, now, in.DateOfBirth, in.IsBanned))
	if err != nil {
		return nil, httperror.Internal(err)
	}
	if err := s.audit.Record(ctx, actor, "person.create", "person", p.ID, map[string]any{
		"legal_name": p.LegalName, "email": p.Email,
	}); err != nil {
		return nil, err
	}
	return p, nil
}

// Update replaces every writeable field of a person.
func (s *PeopleService) Update(ctx context.Context, actor string, id uuid.UUID, in PersonInput) (*domain.Person, error) {
	if issues := in.normalize(); len(issues) > 0 {
		return nil, httperror.Validation("validation failed", issues)
	}
	if err := s.ensureEmailFree(ctx, in.Email, id); err != nil {
		return nil, err
	}
	legal, display := in.names()
	socials, _ := json.Marshal(in.SocialHandles)
	emergency, _ := json.Marshal(in.EmergencyContact)

	p, err := scanPerson(s.pool.QueryRow(ctx, `
UPDATE people SET first_name = $2, last_name = $3, nickname = NULLIF($4, ''), legal_name = $5,
       display_name = $6, email = $7, org_email = NULLIF($8, ''), phone = NULLIF($9, ''),
       address = NULLIF($10, ''), social_handles = $11, emergency_contact = $12, status = $13,
       notes = NULLIF($14, ''), date_of_birth = NULLIF($15, '')::date, is_banned = $16, updated_at = now()
 WHERE id = $1
RETURNING `+personCols,
		id, in.FirstName, in.LastName, in.Nickname, legal, display, in.Email, in.OrgEmail,
		in.Phone, in.Address, socials, emergency, in.Status, in.Notes, in.DateOfBirth, in.IsBanned))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, httperror.NotFound("person")
		}
		return nil, httperror.Internal(err)
	}
	if err := s.audit.Record(ctx, actor, "person.update", "person", p.ID, map[string]any{
		"email": p.Email, "status": p.Status,
	}); err != nil {
		return nil, err
	}
	return p, nil
}

// SetStatus moves a person's lifecycle status (used by draft transitions).
func (s *PeopleService) SetStatus(ctx context.Context, id uuid.UUID, status domain.PersonStatus) error {
	if _, err := s.pool.Exec(ctx, `UPDATE people SET status = $2, updated_at = now() WHERE id = $1`, id, string(status)); err != nil {
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

// List returns people ordered by display name, optionally filtered by a
// free-text query (name, nickname, email) and status.
func (s *PeopleService) List(ctx context.Context, query, status string) ([]domain.Person, error) {
	q := "%" + strings.ToLower(strings.TrimSpace(query)) + "%"
	rows, err := s.pool.Query(ctx, `SELECT `+personCols+` FROM people
WHERE ($1 = '%%' OR lower(legal_name) LIKE $1 OR lower(display_name) LIKE $1 OR lower(email) LIKE $1
       OR lower(COALESCE(org_email, '')) LIKE $1)
  AND ($2 = '' OR status = $2)
ORDER BY lower(display_name)`, q, strings.TrimSpace(status))
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
