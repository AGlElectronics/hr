package http

import (
	"net/http"

	"github.com/AGlElectronics/hr/backend/internal/httperror"
	"github.com/AGlElectronics/hr/backend/internal/service"
)

type notifyHandler struct {
	svc *service.NotifyService
}

func (h *notifyHandler) send(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	var in service.NotifyInput
	// Notify body is optional; swallow EOF so callers can POST with no body.
	if r.ContentLength != 0 && r.Header.Get("Content-Type") != "" {
		if err := decodeJSON(r, &in); err != nil {
			httperror.Render(w, r, err)
			return
		}
	}
	actor := actorFrom(r)
	receipt, err := h.svc.Notify(r.Context(), actor, id, in)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, receipt)
}
