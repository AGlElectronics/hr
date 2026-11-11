package http

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/AGlElectronics/hr/backend/internal/actor"
	"github.com/AGlElectronics/hr/backend/internal/authz"
	"github.com/AGlElectronics/hr/backend/internal/httperror"
	"github.com/AGlElectronics/hr/backend/internal/service"
)

type reviewHandler struct {
	svc   *service.ReviewService
	depts *service.DepartmentService
	chk   *authz.Checker
}

// item loads an item the caller may review: HR admins every item, leads
// the items of departments they lead.
func (h *reviewHandler) item(w http.ResponseWriter, r *http.Request) (*service.ReviewItem, bool) {
	id, err := parseUUIDParam(r, "id")
	if !ok(w, r, err) {
		return nil, false
	}
	it, err := h.svc.Get(r.Context(), id)
	if !ok(w, r, err) {
		return nil, false
	}
	a, _ := actor.FromContext(r.Context())
	if authz.Global(a, authz.ReviewManage) || (authz.Scoped(a, authz.ReviewManage) && it.DepartmentID != nil && a.Leads(*it.DepartmentID)) {
		return it, true
	}
	httperror.Render(w, r, httperror.Forbidden("this item is not in a department you lead"))
	return nil, false
}

func (h *reviewHandler) list(w http.ResponseWriter, r *http.Request) {
	depts, good := scope(w, r, authz.ReviewManage)
	if !good {
		return
	}
	batch, err := queryUUID(r, "import_batch_id")
	if !ok(w, r, err) {
		return
	}
	q := r.URL.Query()
	page, err := h.svc.List(r.Context(), service.ReviewFilter{Status: q.Get("status"), Source: q.Get("source"), Query: q.Get("q"),
		DepartmentIDs: depts, ImportBatchID: batch, Limit: queryInt(r, "limit"), Offset: queryInt(r, "offset")})
	if !ok(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (h *reviewHandler) get(w http.ResponseWriter, r *http.Request) {
	if it, good := h.item(w, r); good {
		writeJSON(w, http.StatusOK, it)
	}
}

func (h *reviewHandler) update(w http.ResponseWriter, r *http.Request) {
	it, good := h.item(w, r)
	if !good {
		return
	}
	var in service.ReviewUpdateInput
	if !ok(w, r, decodeJSON(r, &in)) {
		return
	}
	if in.DepartmentID != nil {
		if !ok(w, r, h.chk.Department(r.Context(), authz.ReviewManage, *in.DepartmentID)) {
			return
		}
	} else if !ok(w, r, h.chk.Require(r.Context(), authz.ReviewManage)) {
		return
	}
	out, err := h.svc.Update(r.Context(), actorFrom(r), it.ID, in)
	if !ok(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *reviewHandler) accept(w http.ResponseWriter, r *http.Request) {
	it, good := h.item(w, r)
	if !good {
		return
	}
	var in service.AcceptInput
	if !ok(w, r, decodeJSON(r, &in)) || !ok(w, r, h.chk.Department(r.Context(), authz.OnboardingManage, in.DepartmentID)) {
		return
	}
	if in.Complete && !ok(w, r, h.chk.Department(r.Context(), authz.AccessManage, in.DepartmentID)) {
		return
	}
	res, err := h.svc.Accept(r.Context(), actorFrom(r), it.ID, in)
	if !ok(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *reviewHandler) merge(w http.ResponseWriter, r *http.Request) {
	it, good := h.item(w, r)
	if !good {
		return
	}
	var in struct {
		PersonID uuid.UUID `json:"person_id"`
		Note     string    `json:"note"`
	}
	if !ok(w, r, decodeJSON(r, &in)) || !ok(w, r, h.chk.Person(r.Context(), authz.PeopleWrite, in.PersonID)) ||
		!ok(w, r, authz.NotSelf(r.Context(), in.PersonID, "account")) {
		return
	}
	out, err := h.svc.Merge(r.Context(), actorFrom(r), it.ID, in.PersonID, in.Note)
	if !ok(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *reviewHandler) reject(w http.ResponseWriter, r *http.Request) {
	it, good := h.item(w, r)
	if !good {
		return
	}
	var in struct {
		Reason string `json:"reason"`
	}
	if r.ContentLength != 0 && !ok(w, r, decodeJSON(r, &in)) {
		return
	}
	out, warnings, err := h.svc.Reject(r.Context(), actorFrom(r), it.ID, in.Reason)
	if !ok(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"item": out, "warnings": warnings})
}

func (h *reviewHandler) scan(w http.ResponseWriter, r *http.Request) {
	res, err := h.svc.Scan(r.Context())
	if !ok(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// selfGet and selfSubmit serve the "waiting for a coordinator" form: any
// signed-in person, only their own entry.
func (h *reviewHandler) selfGet(w http.ResponseWriter, r *http.Request) {
	v, err := h.svc.SelfService(r.Context())
	if !ok(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (h *reviewHandler) selfSubmit(w http.ResponseWriter, r *http.Request) {
	var in service.SelfServiceInput
	if !ok(w, r, decodeJSON(r, &in)) {
		return
	}
	v, err := h.svc.SubmitSelfService(r.Context(), in)
	if !ok(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (h *reviewHandler) selfDepartments(w http.ResponseWriter, r *http.Request) {
	depts, err := h.depts.ListActive(r.Context())
	if !ok(w, r, err) {
		return
	}
	out := make([]map[string]string, 0, len(depts))
	for _, d := range depts {
		out = append(out, map[string]string{"id": d.ID.String(), "name": d.Name, "description": d.Description})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}
