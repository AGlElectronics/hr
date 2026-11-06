package http

import (
	"net/http"

	"github.com/AGlElectronics/hr/backend/internal/httperror"
	"github.com/AGlElectronics/hr/backend/internal/qualification"
	"github.com/AGlElectronics/hr/backend/internal/service"
)

type roleGateHandler struct {
	svc   *service.RoleGateService
	quals *qualification.Fetcher
}

func (h *roleGateHandler) qualificationCatalog(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"modules": h.quals.FetchAll(r.Context())})
}

func (h *roleGateHandler) listRequirements(w http.ResponseWriter, r *http.Request) {
	reqs, err := h.svc.ListRequirements(r.Context())
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"active": h.svc.Active(), "items": reqs})
}

func (h *roleGateHandler) createRequirement(w http.ResponseWriter, r *http.Request) {
	var in service.RoleRequirementInput
	if err := decodeJSON(r, &in); err != nil {
		httperror.Render(w, r, err)
		return
	}
	req, err := h.svc.AddRequirement(r.Context(), actorFrom(r), in)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, req)
}

func (h *roleGateHandler) deleteRequirement(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	if err := h.svc.DeleteRequirement(r.Context(), actorFrom(r), id); err != nil {
		httperror.Render(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *roleGateHandler) listGrants(w http.ResponseWriter, r *http.Request) {
	grants, err := h.svc.ListGrants(r.Context(), r.URL.Query().Get("status"))
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"active": h.svc.Active(), "items": grants})
}

func (h *roleGateHandler) override(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	var in service.OverrideInput
	if err := decodeJSON(r, &in); err != nil {
		httperror.Render(w, r, err)
		return
	}
	g, err := h.svc.Override(r.Context(), actorFrom(r), id, in)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, g)
}
