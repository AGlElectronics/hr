package service

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AGlElectronics/hr/backend/internal/domain"
	"github.com/AGlElectronics/hr/backend/internal/httperror"
)

// DashboardService aggregates what the HR home page needs in one call.
type DashboardService struct {
	pool   *pgxpool.Pool
	people *PeopleService
	depts  *DepartmentService
}

// NewDashboardService wires a DashboardService.
func NewDashboardService(pool *pgxpool.Pool, people *PeopleService, depts *DepartmentService) *DashboardService {
	return &DashboardService{pool: pool, people: people, depts: depts}
}

// AttentionItem is one thing HR should look at.
type AttentionItem struct {
	Kind        string     `json:"kind"` // incomplete_profile | failed_provisioning | stalled_onboarding
	PersonID    uuid.UUID  `json:"person_id"`
	DisplayName string     `json:"display_name"`
	LegalName   string     `json:"legal_name"`
	Email       string     `json:"email"`
	DraftID     *uuid.UUID `json:"draft_id,omitempty"`
	// Detail is a short, human-readable summary.
	Detail string `json:"detail"`
	// Fields lists the incomplete or invalid profile fields.
	Fields []string `json:"fields,omitempty"`
	// Technical is the raw error for failed provisioning (shown under Details).
	Technical string `json:"technical,omitempty"`
}

// DepartmentSummary is a department with its current member count.
type DepartmentSummary struct {
	domain.Department
	ActiveMembers int `json:"active_members"`
	InOnboarding  int `json:"in_onboarding"`
}

// Dashboard is the HR home page payload.
type Dashboard struct {
	StatusCounts map[string]int      `json:"status_counts"`
	Attention    []AttentionItem     `json:"attention"`
	Departments  []DepartmentSummary `json:"departments"`
}

// Get builds the dashboard. scope nil shows everything; otherwise only the
// people and departments of these departments (department leads).
func (s *DashboardService) Get(ctx context.Context, scope []uuid.UUID) (*Dashboard, error) {
	all, err := s.people.All(ctx)
	if err != nil {
		return nil, err
	}
	people := all
	if scope != nil {
		inScope := map[uuid.UUID]bool{}
		rows, err := s.pool.Query(ctx, `
SELECT DISTINCT person_id FROM employment_drafts WHERE department_id = ANY($1) AND state <> 'rejected'`, scope)
		if err != nil {
			return nil, httperror.Internal(err)
		}
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, httperror.Internal(err)
			}
			inScope[id] = true
		}
		rows.Close()
		people = people[:0:0]
		for _, p := range all {
			if inScope[p.ID] {
				people = append(people, p)
			}
		}
	}
	out := &Dashboard{StatusCounts: map[string]int{}, Attention: []AttentionItem{}, Departments: []DepartmentSummary{}}
	byID := map[uuid.UUID]*domain.Person{}
	for i := range people {
		p := &people[i]
		byID[p.ID] = p
		out.StatusCounts[string(p.Status)]++
		if missing := MissingFields(p); len(missing) > 0 {
			fields := make([]string, 0, len(missing))
			for f := range missing {
				fields = append(fields, fieldLabel(f))
			}
			sort.Strings(fields)
			out.Attention = append(out.Attention, AttentionItem{Kind: "incomplete_profile", PersonID: p.ID, DisplayName: p.DisplayName,
				LegalName: p.LegalName, Email: p.Email, Fields: fields, Detail: missingSummary(fields)})
		}
	}

	rows, err := s.pool.Query(ctx, `
SELECT d.id, d.person_id,
       COALESCE((SELECT j.message FROM provisioning_jobs j WHERE j.draft_id = d.id AND j.status = 'failed'
                 ORDER BY j.started_at DESC LIMIT 1), ''),
       d.updated_at < now() - interval '7 days'
  FROM employment_drafts d
 WHERE d.state = 'draft' AND d.department_id IS NOT NULL`)
	if err != nil {
		return nil, httperror.Internal(err)
	}
	for rows.Next() {
		var it AttentionItem
		var failed string
		var stale bool
		if err := rows.Scan(&it.DraftID, &it.PersonID, &failed, &stale); err != nil {
			rows.Close()
			return nil, httperror.Internal(err)
		}
		p := byID[it.PersonID]
		if p == nil {
			continue
		}
		it.DisplayName, it.LegalName, it.Email = p.DisplayName, p.LegalName, p.Email
		switch {
		case failed != "":
			it.Kind, it.Detail, it.Technical = "failed_provisioning", friendlyFailure(failed), failed
		case stale:
			it.Kind, it.Detail = "stalled_onboarding", "Onboarding untouched for over a week"
		default:
			continue
		}
		out.Attention = append(out.Attention, it)
	}
	rows.Close()

	depts, err := s.depts.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, d := range depts {
		if scope != nil && !containsID(scope, d.ID) {
			continue
		}
		sum := DepartmentSummary{Department: d}
		if err := s.pool.QueryRow(ctx, `
SELECT COUNT(DISTINCT p.id) FILTER (WHERE dr.state = 'active' AND p.status IN ('active','paused')),
       COUNT(DISTINCT p.id) FILTER (WHERE dr.state = 'draft')
  FROM employment_drafts dr JOIN people p ON p.id = dr.person_id
 WHERE dr.department_id = $1`, d.ID).Scan(&sum.ActiveMembers, &sum.InOnboarding); err != nil {
			return nil, httperror.Internal(err)
		}
		out.Departments = append(out.Departments, sum)
	}
	return out, nil
}

func containsID(ids []uuid.UUID, id uuid.UUID) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

var fieldLabels = map[string]string{
	"first_name": "first name", "last_name": "last name", "nickname": "nickname", "date_of_birth": "date of birth",
	"email": "email", "org_email": "organization email", "phone": "phone",
	"address.street": "street", "address.postal_code": "postal code", "address.city": "city", "address.country": "country",
	"emergency_contact.name": "emergency contact name", "emergency_contact.phone": "emergency contact phone",
	"emergency_contact.email": "emergency contact email",
}

func fieldLabel(f string) string {
	if l, ok := fieldLabels[f]; ok {
		return l
	}
	return strings.ReplaceAll(strings.ReplaceAll(f, ".", " "), "_", " ")
}

// missingSummary shortens long field lists: "Missing phone, city +3 more".
func missingSummary(fields []string) string {
	const shown = 2
	if len(fields) <= shown+1 {
		return "Missing or invalid: " + strings.Join(fields, ", ")
	}
	return "Missing or invalid: " + strings.Join(fields[:shown], ", ") + " +" + strconv.Itoa(len(fields)-shown) + " more"
}

// friendlyFailure turns a connector error into one plain sentence.
func friendlyFailure(msg string) string {
	m := strings.ToLower(msg)
	switch {
	case strings.Contains(m, "already used by another account"):
		return "The login name is already taken. Change their nickname and try again."
	case strings.Contains(m, "unreachable") || strings.Contains(m, "connection refused") || strings.Contains(m, "no such host"):
		return "The account service couldn't be reached. Try again in a moment."
	case strings.Contains(m, "token") || strings.Contains(m, "403") || strings.Contains(m, "forbidden"):
		return "HR isn't allowed to create accounts right now. Ask an admin to check the account service setup."
	case strings.Contains(m, "email"):
		return "Their account couldn't be set up because of their email address. Check it and try again."
	default:
		return "Their account couldn't be created. Open it to retry."
	}
}
