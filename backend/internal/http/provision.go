package http

import (
	"net/http"

	"github.com/AGlElectronics/hr/backend/internal/authz"
	"github.com/AGlElectronics/hr/backend/internal/service"
)

type provisionHandler struct {
	svc *service.ProvisionService
	chk *authz.Checker
}

func (h *provisionHandler) listJobs(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "id")
	if !ok(w, r, err) || !ok(w, r, h.chk.Draft(r.Context(), authz.PeopleRead, id)) {
		return
	}
	jobs, err := h.svc.ListJobs(r.Context(), id)
	if !ok(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": jobs})
}
