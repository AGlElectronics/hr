package service

import (
	"context"
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
	Kind        string    `json:"kind"` // missing_email | incomplete_profile | failed_provisioning | stalled_onboarding
	PersonID    uuid.UUID `json:"person_id"`
	DisplayName string    `json:"display_name"`
	DraftID     *uuid.UUID `json:"draft_id,omitempty"`
	Detail      string    `json:"detail"`
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

// Get builds the dashboard.
func (s *DashboardService) Get(ctx context.Context) (*Dashboard, error) {
	people, err := s.people.List(ctx, "", "")
	if err != nil {
		return nil, err
	}
	out := &Dashboard{StatusCounts: map[string]int{}, Attention: []AttentionItem{}, Departments: []DepartmentSummary{}}
	for i := range people {
		p := &people[i]
		out.StatusCounts[string(p.Status)]++
		if p.Status == domain.PersonOffboarded {
			continue
		}
		if p.EmailMissing {
			out.Attention = append(out.Attention, AttentionItem{Kind: "missing_email", PersonID: p.ID, DisplayName: p.DisplayName,
				Detail: "No email yet — required for login and onboarding"})
			continue
		}
		if missing := MissingFields(p); len(missing) > 0 {
			fields := make([]string, 0, len(missing))
			for f := range missing {
				fields = append(fields, strings.ReplaceAll(strings.ReplaceAll(f, ".", " "), "_", " "))
			}
			out.Attention = append(out.Attention, AttentionItem{Kind: "incomplete_profile", PersonID: p.ID, DisplayName: p.DisplayName,
				Detail: "Missing: " + joinSorted(fields)})
		}
	}

	rows, err := s.pool.Query(ctx, `
SELECT d.id, d.person_id, p.display_name,
       COALESCE((SELECT j.message FROM provisioning_jobs j WHERE j.draft_id = d.id AND j.status = 'failed'
                 ORDER BY j.started_at DESC LIMIT 1), ''),
       d.updated_at < now() - interval '7 days'
  FROM employment_drafts d JOIN people p ON p.id = d.person_id
 WHERE d.state = 'draft' AND d.department_id IS NOT NULL AND p.status <> 'offboarded'`)
	if err != nil {
		return nil, httperror.Internal(err)
	}
	for rows.Next() {
		var it AttentionItem
		var failed string
		var stale bool
		if err := rows.Scan(&it.DraftID, &it.PersonID, &it.DisplayName, &failed, &stale); err != nil {
			rows.Close()
			return nil, httperror.Internal(err)
		}
		switch {
		case failed != "":
			it.Kind, it.Detail = "failed_provisioning", "Last attempt failed: "+failed
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

func joinSorted(ss []string) string {
	for i := 1; i < len(ss); i++ {
		for j := i; j > 0 && ss[j] < ss[j-1]; j-- {
			ss[j], ss[j-1] = ss[j-1], ss[j]
		}
	}
	out := ""
	for i, s := range ss {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}
