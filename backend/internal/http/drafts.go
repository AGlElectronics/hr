package http

import (
	"net/http"

	"github.com/AGlElectronics/hr/backend/internal/httperror"
	"github.com/AGlElectronics/hr/backend/internal/service"
)

type draftHandler struct {
	svc *service.DraftService
}

func (h *draftHandler) list(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	items, err := h.svc.List(r.Context(), state)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *draftHandler) get(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	d, err := h.svc.Get(r.Context(), id)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (h *draftHandler) create(w http.ResponseWriter, r *http.Request) {
	var in service.CreateDraftInput
	if err := decodeJSON(r, &in); err != nil {
		httperror.Render(w, r, err)
		return
	}
	actor := actorFrom(r)
	d, err := h.svc.Create(r.Context(), actor, in)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, d)
}

func (h *draftHandler) assignRoles(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	var in service.AssignRolesInput
	if err := decodeJSON(r, &in); err != nil {
		httperror.Render(w, r, err)
		return
	}
	actor := actorFrom(r)
	roles, err := h.svc.AssignRoles(r.Context(), actor, id, in)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"roles": roles})
}
