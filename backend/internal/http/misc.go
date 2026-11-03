package http

import (
	"net/http"

	"github.com/AGlElectronics/hr/backend/internal/httperror"
	"github.com/AGlElectronics/hr/backend/internal/provisioners"
	"github.com/AGlElectronics/hr/backend/internal/service"
)

type miscHandler struct {
	settings  *service.SettingsService
	dash      *service.DashboardService
	directory provisioners.Directory // nil when the IdP cannot list groups
	depts     *service.DepartmentService
}

func (h *miscHandler) getSettings(w http.ResponseWriter, r *http.Request) {
	s, err := h.settings.Get(r.Context())
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s)
}

func (h *miscHandler) updateSettings(w http.ResponseWriter, r *http.Request) {
	var in service.Settings
	if err := decodeJSON(r, &in); err != nil {
		httperror.Render(w, r, err)
		return
	}
	s, err := h.settings.Update(r.Context(), actorFrom(r), in)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s)
}

func (h *miscHandler) dashboard(w http.ResponseWriter, r *http.Request) {
	d, err := h.dash.Get(r.Context())
	if err != nil {
		httperror.Render(w, r, err)
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

// idpGroups is a read-only view of the IdP's groups and members, annotated
// with what HR uses each group for.
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
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	out := make([]idpGroupView, 0, len(groups))
	for _, g := range groups {
		v := idpGroupView{GroupInfo: g}
		for _, d := range depts {
			if d.GroupName == g.Name {
				v.Department = &struct {
					ID   string `json:"id"`
					Name string `json:"name"`
				}{ID: d.ID.String(), Name: d.Name}
			}
			for _, role := range d.DefaultRoles {
				if role.GroupName == g.Name {
					v.DefaultFor = append(v.DefaultFor, d.Name)
				}
			}
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, map[string]any{"available": true, "groups": out})
}
