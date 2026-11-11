package service

import (
	"context"

	"github.com/google/uuid"

	"github.com/AGlElectronics/hr/backend/internal/domain"
	"github.com/AGlElectronics/hr/backend/internal/httperror"
)

// OnboardingService drives the HR onboarding wizard on top of people,
// departments and employment drafts:
//
//	page 1  Start / UpdateDetails  volunteer profile + department
//	page 2  DraftService.AssignRoles (prefilled with the department defaults)
//	page 3  View + ProvisionService.Complete
type OnboardingService struct {
	people *PeopleService
	drafts *DraftService
	depts  *DepartmentService
	// suiteDefaults returns the roles modules mark as on by default (nil-safe).
	suiteDefaults func(ctx context.Context) []RolePlanInput
	gate          *RoleGateService  // nil = no training requirements
	prov          *ProvisionService // nil: no provisioning status
}

// SetRoleGate shows roles waiting for training on the overview.
func (s *OnboardingService) SetRoleGate(g *RoleGateService) { s.gate = g }

// NewOnboardingService wires an OnboardingService. suiteDefaults may be nil.
func NewOnboardingService(people *PeopleService, drafts *DraftService, depts *DepartmentService, suiteDefaults func(ctx context.Context) []RolePlanInput) *OnboardingService {
	return &OnboardingService{people: people, drafts: drafts, depts: depts, suiteDefaults: suiteDefaults}
}

// OnboardingDetailsInput is page 1 of the wizard.
type OnboardingDetailsInput struct {
	// PersonID onboards someone HR already knows; empty creates a new person.
	PersonID     uuid.UUID   `json:"person_id,omitempty"`
	Person       PersonInput `json:"person"`
	DepartmentID uuid.UUID   `json:"department_id"`
}

// OnboardingView is everything the wizard pages need for one onboarding.
type OnboardingView struct {
	Draft         *domain.EmploymentDraft `json:"draft"`
	Person        *domain.Person          `json:"person"`
	Department    *domain.Department      `json:"department,omitempty"`
	MissingFields map[string]string       `json:"missing_fields"`
	// PendingGrants are roles from this onboarding still waiting for training.
	PendingGrants []PendingGrant `json:"pending_grants"`
	// Provisioning is the latest "Complete" job (nil before the first one).
	Provisioning *ProvisioningStatus `json:"provisioning,omitempty"`
}

// SetProvisioning shows the completion job's status on the overview.
func (s *OnboardingService) SetProvisioning(p *ProvisionService) { s.prov = p }

// Cancel rejects an onboarding that is still a draft. The person goes back
// to applicant unless they have another onboarding or membership.
func (s *OnboardingService) Cancel(ctx context.Context, actor string, draftID uuid.UUID, reason string) (*OnboardingView, error) {
	d, err := s.drafts.Get(ctx, draftID)
	if err != nil {
		return nil, err
	}
	if d.State != domain.DraftStateDraft {
		return nil, httperror.Conflict("only an onboarding that is still a draft can be cancelled (it is " + string(d.State) + ")")
	}
	if _, err := s.drafts.Transition(ctx, draftID, domain.DraftStateRejected); err != nil {
		return nil, err
	}
	p, err := s.people.Get(ctx, d.PersonID)
	if err != nil {
		return nil, err
	}
	if p.Status == domain.PersonOnboarding {
		var others bool
		if err := s.people.pool.QueryRow(ctx, `
SELECT EXISTS (SELECT 1 FROM employment_drafts WHERE person_id = $1 AND id <> $2 AND state IN ('draft', 'provisioning', 'active'))`,
			d.PersonID, draftID).Scan(&others); err != nil {
			return nil, httperror.Internal(err)
		}
		if !others {
			if err := s.people.SetStatus(ctx, d.PersonID, domain.PersonApplicant); err != nil {
				return nil, err
			}
		}
	}
	if err := s.drafts.audit.Record(ctx, actor, "onboarding.cancel", "employment_draft", draftID, map[string]any{
		"person_id": d.PersonID, "reason": reason,
	}); err != nil {
		return nil, err
	}
	return s.View(ctx, draftID)
}

func (s *OnboardingService) requireDepartment(ctx context.Context, id uuid.UUID) (*domain.Department, error) {
	if id == uuid.Nil {
		return nil, httperror.Validation("validation failed", map[string]string{"department_id": "required"})
	}
	d, err := s.depts.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if d.ArchivedAt != nil {
		return nil, httperror.Validation("validation failed", map[string]string{"department_id": "this department is archived"})
	}
	return d, nil
}

// applyDefaults sets the access plan to the suite-wide default roles plus
// the department's defaults.
func (s *OnboardingService) applyDefaults(ctx context.Context, actor string, draftID uuid.UUID, dept *domain.Department) error {
	roles := make([]RolePlanInput, 0, len(dept.DefaultRoles)+2)
	seen := map[string]bool{}
	if s.suiteDefaults != nil {
		for _, r := range s.suiteDefaults(ctx) {
			seen[r.RoleName] = true
			roles = append(roles, r)
		}
	}
	for _, r := range dept.DefaultRoles {
		if seen[r.RoleName] {
			continue
		}
		roles = append(roles, RolePlanInput{RoleName: r.RoleName, GroupName: r.GroupName, Scope: r.Scope})
	}
	_, err := s.drafts.AssignDefaults(ctx, actor, draftID, AssignRolesInput{Roles: roles, BundleIDs: dept.DefaultBundles})
	return err
}

// Start saves page 1 for a new volunteer: creates the person and an
// onboarding draft in the chosen department, prefilled with the
// department's default access.
func (s *OnboardingService) Start(ctx context.Context, actor string, in OnboardingDetailsInput) (*OnboardingView, error) {
	dept, err := s.requireDepartment(ctx, in.DepartmentID)
	if err != nil {
		return nil, err
	}
	in.Person.Status = string(domain.PersonOnboarding)
	var person *domain.Person
	if in.PersonID != uuid.Nil {
		existing, err := s.people.Get(ctx, in.PersonID)
		if err != nil {
			return nil, err
		}
		if existing.IsBanned {
			return nil, httperror.Conflict("this person is banned and cannot be onboarded")
		}
		in.Person.Notes = firstNonEmpty(in.Person.Notes, existing.Notes)
		person, err = s.people.Update(ctx, actor, in.PersonID, in.Person)
		if err != nil {
			return nil, err
		}
	} else {
		person, err = s.people.Create(ctx, actor, in.Person)
		if err != nil {
			return nil, err
		}
	}
	draft, err := s.drafts.Create(ctx, actor, CreateDraftInput{PersonID: person.ID, DepartmentID: &dept.ID})
	if err != nil {
		return nil, err
	}
	if err := s.applyDefaults(ctx, actor, draft.ID, dept); err != nil {
		return nil, err
	}
	return s.View(ctx, draft.ID)
}

// UpdateDetails re-saves page 1 of an existing onboarding. Switching
// department resets the access plan to the new department's defaults.
func (s *OnboardingService) UpdateDetails(ctx context.Context, actor string, draftID uuid.UUID, in OnboardingDetailsInput) (*OnboardingView, error) {
	draft, err := s.drafts.Get(ctx, draftID)
	if err != nil {
		return nil, err
	}
	if draft.State != domain.DraftStateDraft {
		return nil, httperror.Conflict("this onboarding is no longer editable")
	}
	dept, err := s.requireDepartment(ctx, in.DepartmentID)
	if err != nil {
		return nil, err
	}
	in.Person.Status = string(domain.PersonOnboarding)
	if _, err := s.people.Update(ctx, actor, draft.PersonID, in.Person); err != nil {
		return nil, err
	}
	if draft.DepartmentID == nil || *draft.DepartmentID != dept.ID {
		if _, err := s.drafts.SetDepartment(ctx, actor, draftID, dept.ID); err != nil {
			return nil, err
		}
		if err := s.applyDefaults(ctx, actor, draftID, dept); err != nil {
			return nil, err
		}
	}
	return s.View(ctx, draftID)
}

// View loads the draft, person and department for one onboarding.
func (s *OnboardingService) View(ctx context.Context, draftID uuid.UUID) (*OnboardingView, error) {
	draft, err := s.drafts.Get(ctx, draftID)
	if err != nil {
		return nil, err
	}
	person, err := s.people.Get(ctx, draft.PersonID)
	if err != nil {
		return nil, err
	}
	v := &OnboardingView{Draft: draft, Person: person, MissingFields: MissingFields(person), PendingGrants: []PendingGrant{}}
	if draft.DepartmentID != nil {
		if v.Department, err = s.depts.Get(ctx, *draft.DepartmentID); err != nil {
			return nil, err
		}
	}
	if s.prov != nil {
		if v.Provisioning, err = s.prov.Status(ctx, draftID); err != nil {
			return nil, err
		}
	}
	if s.gate != nil && draft.State == domain.DraftStateActive {
		waiting, err := s.gate.WaitingForPerson(ctx, person.ID)
		if err != nil {
			return nil, err
		}
		for _, g := range waiting {
			if g.DraftID != nil && *g.DraftID == draft.ID {
				v.PendingGrants = append(v.PendingGrants, g)
			}
		}
	}
	return v, nil
}
