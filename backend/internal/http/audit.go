package http

import (
	"net/http"
	"time"

	"github.com/AGlElectronics/hr/backend/internal/httperror"
	"github.com/AGlElectronics/hr/backend/internal/service"
)

type auditHandler struct {
	svc *service.AuditService
}

func queryTime(r *http.Request, name string) (*time.Time, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		if d, derr := time.Parse("2006-01-02", raw); derr == nil {
			return &d, nil
		}
		return nil, httperror.BadRequest(name + " must be a date (2006-01-02) or RFC 3339 time")
	}
	return &t, nil
}

func (h *auditHandler) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	entity, err := queryUUID(r, "entity_id")
	if !ok(w, r, err) {
		return
	}
	since, err := queryTime(r, "since")
	if !ok(w, r, err) {
		return
	}
	before, err := queryTime(r, "before")
	if !ok(w, r, err) {
		return
	}
	page, err := h.svc.List(r.Context(), service.AuditFilter{
		EntityType: q.Get("entity_type"), EntityID: entity, Actor: q.Get("actor"), Action: q.Get("action"),
		Since: since, Before: before, Limit: queryInt(r, "limit"), Offset: queryInt(r, "offset"),
	})
	if !ok(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, page)
}
