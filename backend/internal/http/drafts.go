package http

import (
	"net/http"

	"github.com/AGlElectronics/hr/backend/internal/authz"
	"github.com/AGlElectronics/hr/backend/internal/service"
)

type draftHandler struct {
	svc *service.DraftService
	chk *authz.Checker
}

func (h *draftHandler) list(w http.ResponseWriter, r *http.Request) {
	depts, good := scope(w, r, authz.PeopleRead)
	if !good {
		return
	}
	q := r.URL.Query()
	page, err := h.svc.List(r.Context(), service.ListDraftsInput{
		State: q.Get("state"), Query: q.Get("q"), DepartmentIDs: depts,
		Limit: queryInt(r, "limit"), Offset: queryInt(r, "offset"),
	})
	if !ok(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (h *draftHandler) get(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "id")
	if !ok(w, r, err) || !ok(w, r, h.chk.Draft(r.Context(), authz.PeopleRead, id)) {
		return
	}
	d, err := h.svc.Get(r.Context(), id)
	if !ok(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (h *draftHandler) create(w http.ResponseWriter, r *http.Request) {
	var in service.CreateDraftInput
	if !ok(w, r, decodeJSON(r, &in)) {
		return
	}
	if in.DepartmentID != nil {
		if !ok(w, r, h.chk.Department(r.Context(), authz.OnboardingManage, *in.DepartmentID)) {
			return
		}
	} else if !ok(w, r, h.chk.Require(r.Context(), authz.OnboardingManage)) {
		return
	}
	if !ok(w, r, authz.NotSelf(r.Context(), in.PersonID, "onboarding")) {
		return
	}
	d, err := h.svc.Create(r.Context(), actorFrom(r), in)
	if !ok(w, r, err) {
		return
	}
	writeJSON(w, http.StatusCreated, d)
}

func (h *draftHandler) assignRoles(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "id")
	if !ok(w, r, err) || !ok(w, r, h.chk.Draft(r.Context(), authz.OnboardingManage, id)) {
		return
	}
	d, err := h.svc.Get(r.Context(), id)
	if !ok(w, r, err) || !ok(w, r, authz.NotSelf(r.Context(), d.PersonID, "access")) {
		return
	}
	var in service.AssignRolesInput
	if !ok(w, r, decodeJSON(r, &in)) {
		return
	}
	roles, err := h.svc.AssignRoles(r.Context(), actorFrom(r), id, in)
	if !ok(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"roles": roles})
}
