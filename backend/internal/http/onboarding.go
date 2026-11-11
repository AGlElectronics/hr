package http

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/AGlElectronics/hr/backend/internal/authz"
	"github.com/AGlElectronics/hr/backend/internal/service"
)

type onboardingHandler struct {
	svc  *service.OnboardingService
	prov *service.ProvisionService
	chk  *authz.Checker
}

func (h *onboardingHandler) start(w http.ResponseWriter, r *http.Request) {
	var in service.OnboardingDetailsInput
	if !ok(w, r, decodeJSON(r, &in)) || !ok(w, r, h.chk.Department(r.Context(), authz.OnboardingManage, in.DepartmentID)) {
		return
	}
	if in.PersonID != uuid.Nil {
		if !ok(w, r, h.chk.Person(r.Context(), authz.PeopleWrite, in.PersonID)) || !ok(w, r, authz.NotSelf(r.Context(), in.PersonID, "onboarding")) {
			return
		}
	}
	v, err := h.svc.Start(r.Context(), actorFrom(r), in)
	if !ok(w, r, err) {
		return
	}
	writeJSON(w, http.StatusCreated, v)
}

func (h *onboardingHandler) get(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "id")
	if !ok(w, r, err) || !ok(w, r, h.chk.Draft(r.Context(), authz.PeopleRead, id)) {
		return
	}
	v, err := h.svc.View(r.Context(), id)
	if !ok(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// own loads the onboarding after checking p on it, and refuses the caller's
// own onboarding.
func (h *onboardingHandler) own(w http.ResponseWriter, r *http.Request, p authz.Permission) (uuid.UUID, bool) {
	id, err := parseUUIDParam(r, "id")
	if !ok(w, r, err) || !ok(w, r, h.chk.Draft(r.Context(), p, id)) {
		return uuid.Nil, false
	}
	v, err := h.svc.View(r.Context(), id)
	if !ok(w, r, err) || !ok(w, r, authz.NotSelf(r.Context(), v.Person.ID, "onboarding")) {
		return uuid.Nil, false
	}
	return id, true
}

func (h *onboardingHandler) updateDetails(w http.ResponseWriter, r *http.Request) {
	id, good := h.own(w, r, authz.OnboardingManage)
	if !good {
		return
	}
	var in service.OnboardingDetailsInput
	if !ok(w, r, decodeJSON(r, &in)) || !ok(w, r, h.chk.Department(r.Context(), authz.OnboardingManage, in.DepartmentID)) {
		return
	}
	v, err := h.svc.UpdateDetails(r.Context(), actorFrom(r), id, in)
	if !ok(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (h *onboardingHandler) complete(w http.ResponseWriter, r *http.Request) {
	id, good := h.own(w, r, authz.AccessManage)
	if !good {
		return
	}
	res, err := h.prov.Complete(r.Context(), actorFrom(r), id)
	if !ok(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, res)
}
