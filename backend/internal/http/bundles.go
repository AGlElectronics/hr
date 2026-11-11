package http

import (
	"net/http"

	"github.com/AGlElectronics/hr/backend/internal/service"
)

type bundleHandler struct {
	svc *service.BundleService
}

func (h *bundleHandler) list(w http.ResponseWriter, r *http.Request) {
	items, err := h.svc.List(r.Context())
	if !ok(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *bundleHandler) get(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "id")
	if !ok(w, r, err) {
		return
	}
	b, err := h.svc.Get(r.Context(), id)
	if !ok(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, b)
}

func (h *bundleHandler) create(w http.ResponseWriter, r *http.Request) {
	var in service.BundleInput
	if !ok(w, r, decodeJSON(r, &in)) {
		return
	}
	b, err := h.svc.Create(r.Context(), actorFrom(r), in)
	if !ok(w, r, err) {
		return
	}
	writeJSON(w, http.StatusCreated, b)
}

func (h *bundleHandler) update(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "id")
	if !ok(w, r, err) {
		return
	}
	var in service.BundleInput
	if !ok(w, r, decodeJSON(r, &in)) {
		return
	}
	b, err := h.svc.Update(r.Context(), actorFrom(r), id, in)
	if !ok(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, b)
}

func (h *bundleHandler) delete(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "id")
	if !ok(w, r, err) || !ok(w, r, h.svc.Delete(r.Context(), actorFrom(r), id)) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
