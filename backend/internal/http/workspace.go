package http

import (
	"net/http"

	"github.com/AGlElectronics/hr/backend/internal/httperror"
	"github.com/AGlElectronics/hr/backend/internal/service"
)

type workspaceHandler struct {
	svc *service.WorkspaceService
}

func (h *workspaceHandler) list(w http.ResponseWriter, r *http.Request) {
	items, err := h.svc.List(r.Context(), r.URL.Query().Get("status"))
	if !ok(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *workspaceHandler) deleteNow(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "id")
	if !ok(w, r, err) {
		return
	}
	a, err := h.svc.DeleteNow(r.Context(), actorFrom(r), id)
	if !ok(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (h *workspaceHandler) sync(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.SyncAllDepartments(r.Context()); err != nil {
		httperror.Render(w, r, httperror.New(httperror.CodeUpstream, "Google Workspace: "+err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"synced": true})
}
