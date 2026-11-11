package http

import (
	"net/http"

	"github.com/AGlElectronics/hr/backend/internal/authz"
	"github.com/AGlElectronics/hr/backend/internal/qualification"
	"github.com/AGlElectronics/hr/backend/internal/service"
)

type roleGateHandler struct {
	svc   *service.RoleGateService
	quals *qualification.Fetcher
	chk   *authz.Checker
}

func (h *roleGateHandler) qualificationCatalog(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"modules": h.quals.FetchAll(r.Context())})
}

func (h *roleGateHandler) listRequirements(w http.ResponseWriter, r *http.Request) {
	reqs, err := h.svc.ListRequirements(r.Context())
	if !ok(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"active": h.svc.Active(), "items": reqs})
}

func (h *roleGateHandler) createRequirement(w http.ResponseWriter, r *http.Request) {
	var in service.RoleRequirementInput
	if !ok(w, r, decodeJSON(r, &in)) {
		return
	}
	req, err := h.svc.AddRequirement(r.Context(), actorFrom(r), in)
	if !ok(w, r, err) {
		return
	}
	writeJSON(w, http.StatusCreated, req)
}

func (h *roleGateHandler) deleteRequirement(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "id")
	if !ok(w, r, err) || !ok(w, r, h.svc.DeleteRequirement(r.Context(), actorFrom(r), id)) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *roleGateHandler) listGrants(w http.ResponseWriter, r *http.Request) {
	depts, good := scope(w, r, authz.PeopleRead)
	if !good {
		return
	}
	grants, err := h.svc.ListGrants(r.Context(), r.URL.Query().Get("status"), depts)
	if !ok(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"active": h.svc.Active(), "items": grants})
}

func (h *roleGateHandler) override(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "id")
	if !ok(w, r, err) {
		return
	}
	person, err := h.svc.GrantPerson(r.Context(), id)
	if !ok(w, r, err) || !ok(w, r, h.chk.Person(r.Context(), authz.TrainingOverride, person)) ||
		!ok(w, r, authz.NotSelf(r.Context(), person, "training requirements")) {
		return
	}
	var in service.OverrideInput
	if !ok(w, r, decodeJSON(r, &in)) {
		return
	}
	g, err := h.svc.Override(r.Context(), actorFrom(r), id, in)
	if !ok(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, g)
}
