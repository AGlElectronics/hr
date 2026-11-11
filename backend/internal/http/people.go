package http

import (
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/AGlElectronics/hr/backend/internal/actor"
	"github.com/AGlElectronics/hr/backend/internal/authz"
	"github.com/AGlElectronics/hr/backend/internal/domain"
	"github.com/AGlElectronics/hr/backend/internal/service"
)

type peopleHandler struct {
	svc       *service.PeopleService
	lifecycle *service.LifecycleService
	chk       *authz.Checker
}

func (h *peopleHandler) list(w http.ResponseWriter, r *http.Request) {
	depts, good := scope(w, r, authz.PeopleRead)
	if !good {
		return
	}
	dept, err := queryUUID(r, "department_id")
	if !ok(w, r, err) {
		return
	}
	q := r.URL.Query()
	page, err := h.svc.List(r.Context(), service.ListPeopleInput{
		Query: q.Get("q"), Status: q.Get("status"), DepartmentIDs: depts, DepartmentID: dept,
		Limit: queryInt(r, "limit"), Offset: queryInt(r, "offset"),
	})
	if !ok(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (h *peopleHandler) get(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "id")
	if !ok(w, r, err) || !ok(w, r, h.chk.Person(r.Context(), authz.PeopleRead, id)) {
		return
	}
	p, err := h.svc.Get(r.Context(), id)
	if !ok(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (h *peopleHandler) create(w http.ResponseWriter, r *http.Request) {
	var in service.PersonInput
	if !ok(w, r, decodeJSON(r, &in)) {
		return
	}
	p, err := h.svc.Create(r.Context(), actorFrom(r), in)
	if !ok(w, r, err) {
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

// update edits the profile. Status and the ban flag are kept as-is: they
// change through onboarding, pause/reactivate, ban and offboarding so the
// IdP stays in sync. Nobody may change profile-critical fields of their own
// record.
func (h *peopleHandler) update(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "id")
	if !ok(w, r, err) || !ok(w, r, h.chk.Person(r.Context(), authz.PeopleWrite, id)) {
		return
	}
	var in service.PersonInput
	if !ok(w, r, decodeJSON(r, &in)) {
		return
	}
	current, err := h.svc.Get(r.Context(), id)
	if !ok(w, r, err) {
		return
	}
	in.Status = string(current.Status)
	in.IsBanned = current.IsBanned
	if critical := service.CriticalChanges(current, in); len(critical) > 0 {
		if !ok(w, r, authz.NotSelf(r.Context(), id, strings.Join(critical, ", "))) {
			return
		}
	}
	p, err := h.svc.Update(r.Context(), actorFrom(r), id, in)
	if !ok(w, r, err) {
		return
	}
	out := struct {
		*domain.Person
		IdPSyncError string `json:"idp_sync_error,omitempty"`
		Warning      string `json:"warning,omitempty"`
	}{Person: p}
	warning, err := h.lifecycle.SyncProfile(r.Context(), current, p)
	if err != nil {
		out.IdPSyncError = err.Error()
	}
	out.Warning = warning
	writeJSON(w, http.StatusOK, out)
}

func (h *peopleHandler) access(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "id")
	if !ok(w, r, err) || !ok(w, r, h.chk.Person(r.Context(), authz.PeopleRead, id)) {
		return
	}
	out, err := h.lifecycle.Access(r.Context(), id)
	if !ok(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *peopleHandler) changeAccess(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "id")
	if !ok(w, r, err) || !ok(w, r, authz.NotSelf(r.Context(), id, "access")) {
		return
	}
	var in service.ChangeAccessInput
	if !ok(w, r, decodeJSON(r, &in)) {
		return
	}
	if in.DraftID != uuid.Nil {
		if !ok(w, r, h.chk.Draft(r.Context(), authz.AccessManage, in.DraftID)) {
			return
		}
	} else if !ok(w, r, h.chk.Require(r.Context(), authz.AccessManage)) {
		return
	}
	out, err := h.lifecycle.ChangeAccess(r.Context(), actorFrom(r), id, in)
	if !ok(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *peopleHandler) setStatus(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "id")
	if !ok(w, r, err) || !ok(w, r, h.chk.Person(r.Context(), authz.PeoplePause, id)) || !ok(w, r, authz.NotSelf(r.Context(), id, "status")) {
		return
	}
	var in service.StatusInput
	if !ok(w, r, decodeJSON(r, &in)) {
		return
	}
	p, err := h.lifecycle.SetStatus(r.Context(), actorFrom(r), id, in)
	if !ok(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (h *peopleHandler) ban(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "id")
	if !ok(w, r, err) || !ok(w, r, authz.NotSelf(r.Context(), id, "ban status")) {
		return
	}
	var in service.BanInput
	if !ok(w, r, decodeJSON(r, &in)) {
		return
	}
	p, err := h.lifecycle.SetBan(r.Context(), actorFrom(r), id, in)
	if !ok(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (h *peopleHandler) resendSetup(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "id")
	if !ok(w, r, err) || !ok(w, r, h.chk.Person(r.Context(), authz.AccessManage, id)) {
		return
	}
	if !ok(w, r, h.lifecycle.ResendPasswordSetup(r.Context(), actorFrom(r), id)) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"sent": true})
}

func (h *peopleHandler) offboard(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "id")
	if !ok(w, r, err) || !ok(w, r, authz.NotSelf(r.Context(), id, "account")) {
		return
	}
	var in service.OffboardInput
	if !ok(w, r, decodeJSON(r, &in)) {
		return
	}
	p, err := h.lifecycle.Offboard(r.Context(), actorFrom(r), id, in)
	if !ok(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func actorFrom(r *http.Request) string {
	return actor.Name(r.Context(), "unknown")
}