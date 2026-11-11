package http

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/AGlElectronics/hr/backend/internal/service"
)

type departmentHandler struct {
	svc *service.DepartmentService
}

func (h *departmentHandler) list(w http.ResponseWriter, r *http.Request) {
	list := h.svc.ListActive
	if r.URL.Query().Get("archived") == "true" {
		list = h.svc.List
	}
	items, err := list(r.Context())
	if !ok(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *departmentHandler) get(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "id")
	if !ok(w, r, err) {
		return
	}
	d, err := h.svc.Get(r.Context(), id)
	if !ok(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (h *departmentHandler) create(w http.ResponseWriter, r *http.Request) {
	var in service.DepartmentInput
	if !ok(w, r, decodeJSON(r, &in)) {
		return
	}
	d, err := h.svc.Create(r.Context(), actorFrom(r), in)
	if !ok(w, r, err) {
		return
	}
	writeJSON(w, http.StatusCreated, d)
}

func (h *departmentHandler) update(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "id")
	if !ok(w, r, err) {
		return
	}
	var in service.DepartmentInput
	if !ok(w, r, decodeJSON(r, &in)) {
		return
	}
	d, err := h.svc.Update(r.Context(), actorFrom(r), id, in)
	if !ok(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (h *departmentHandler) archive(archived bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := parseUUIDParam(r, "id")
		if !ok(w, r, err) {
			return
		}
		d, err := h.svc.SetArchived(r.Context(), actorFrom(r), id, archived)
		if !ok(w, r, err) {
			return
		}
		writeJSON(w, http.StatusOK, d)
	}
}

func (h *departmentHandler) setLeads(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "id")
	if !ok(w, r, err) {
		return
	}
	var in struct {
		PersonIDs []uuid.UUID `json:"person_ids"`
	}
	if !ok(w, r, decodeJSON(r, &in)) {
		return
	}
	res, err := h.svc.SetLeads(r.Context(), actorFrom(r), id, in.PersonIDs)
	if !ok(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, res)
}
