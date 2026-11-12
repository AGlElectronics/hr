package http

import (
	"context"
	"net/http"

	"github.com/AGlElectronics/hr/backend/internal/authz"
	"github.com/AGlElectronics/hr/backend/internal/groupmatch"
	"github.com/AGlElectronics/hr/backend/internal/httperror"
	"github.com/AGlElectronics/hr/backend/internal/provisioners"
	"github.com/AGlElectronics/hr/backend/internal/service"
)

type miscHandler struct {
	settings  *service.SettingsService
	dash      *service.DashboardService
	directory provisioners.Directory // nil when the IDP cannot list groups
	depts     *service.DepartmentService
	// relevantGroups are the groups HR assigns or uses (HR roles, catalog
	// roles, access groups); the group browser shows only these and the
	// department groups.
	relevantGroups func(ctx context.Context) map[string]bool
}

func (h *miscHandler) getSettings(w http.ResponseWriter, r *http.Request) {
	s, err := h.settings.Get(r.Context())
	if !ok(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, s)
}

// updateSettings applies the fields in the body on top of the current
// settings, so a client may send only what it changes.
func (h *miscHandler) updateSettings(w http.ResponseWriter, r *http.Request) {
	in, err := h.settings.Get(r.Context())
	if !ok(w, r, err) || !ok(w, r, decodeJSON(r, in)) {
		return
	}
	s, err := h.settings.Update(r.Context(), actorFrom(r), *in)
	if !ok(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, s)
}

func (h *miscHandler) dashboard(w http.ResponseWriter, r *http.Request) {
	depts, good := scope(w, r, authz.PeopleRead)
	if !good {
		return
	}
	d, err := h.dash.Get(r.Context(), depts)
	if !ok(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, d)
}

type idpGroupView struct {
	provisioners.GroupInfo
	// Department is set when the group belongs to an HR department.
	Department *struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"department,omitempty"`
	// DefaultFor lists departments that grant this group by default.
	DefaultFor []string `json:"default_for,omitempty"`
}

// idpGroups is a read-only view of the IDP groups HR uses and their members,
// annotated with what HR uses each group for. Other realm groups are left
// out.
func (h *miscHandler) idpGroups(w http.ResponseWriter, r *http.Request) {
	if h.directory == nil {
		writeJSON(w, http.StatusOK, map[string]any{"available": false, "groups": []idpGroupView{}})
		return
	}
	groups, err := h.directory.ListGroups(r.Context())
	if err != nil {
		httperror.Render(w, r, httperror.Upstream("Keycloak", err))
		return
	}
	depts, err := h.depts.List(r.Context())
	if !ok(w, r, err) {
		return
	}
	relevant := map[string]bool{}
	if h.relevantGroups != nil {
		relevant = h.relevantGroups(r.Context())
	}
	norm := groupmatch.Normalize
	for _, d := range depts {
		relevant[norm(d.GroupName)], relevant[norm(d.LeadGroup)] = true, true
		for _, role := range d.DefaultRoles {
			relevant[norm(role.GroupName)] = true
		}
	}
	out := make([]idpGroupView, 0, len(groups))
	for _, g := range groups {
		if !relevant[norm(g.Name)] && !relevant[norm(g.Path)] {
			continue
		}
		v := idpGroupView{GroupInfo: g}
		for _, d := range depts {
			if norm(d.GroupName) == norm(g.Name) || norm(d.LeadGroup) == norm(g.Name) {
				v.Department = &struct {
					ID   string `json:"id"`
					Name string `json:"name"`
				}{ID: d.ID.String(), Name: d.Name}
			}
			for _, role := range d.DefaultRoles {
				if norm(role.GroupName) == norm(g.Name) {
					v.DefaultFor = append(v.DefaultFor, d.Name)
				}
			}
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, map[string]any{"available": true, "groups": out})
}
