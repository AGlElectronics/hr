package http

import (
	"net/http"

	"github.com/AGlElectronics/hr/backend/internal/httperror"
	"github.com/AGlElectronics/hr/backend/internal/service"
)

type provisionHandler struct {
	svc *service.ProvisionService
}

func (h *provisionHandler) trigger(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	actor := actorFrom(r)
	draft, jobs, err := h.svc.Trigger(r.Context(), actor, id)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"draft": draft,
		"jobs":  jobs,
	})
}

func (h *provisionHandler) listJobs(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	jobs, err := h.svc.ListJobs(r.Context(), id)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": jobs})
}
