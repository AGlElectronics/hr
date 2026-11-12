package http

import (
	"crypto/subtle"
	"net/http"
	"slices"
	"strings"

	"github.com/AGlElectronics/hr/backend/internal/groupmatch"
	"github.com/AGlElectronics/hr/backend/internal/httperror"
	"github.com/AGlElectronics/hr/backend/internal/service"
)

// DirectoryGroup is one entry of GET /api/internal/directory/groups. Name
// is always an IDP group, so a module can match it against token groups.
type DirectoryGroup struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	// Kind is department (a department's group or its lead group) or
	// access (a group that one or more access groups grant).
	Kind string `json:"kind"`
	// AccessGroups names the HR access groups granting the group (access only).
	AccessGroups []string `json:"access_groups,omitempty"`
}

type directoryHandler struct {
	token   string
	depts   *service.DepartmentService
	bundles *service.BundleService
}

// groups serves other modules (the wiki's share dialog) HR's departments,
// their lead groups and the IDP groups granted by access groups. It needs
// HR_DIRECTORY_TOKEN as a bearer token and is off (404) when that is not set.
func (h *directoryHandler) groups(w http.ResponseWriter, r *http.Request) {
	if h.token == "" {
		httperror.Render(w, r, httperror.NotFound("directory"))
		return
	}
	got, found := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !found || subtle.ConstantTimeCompare([]byte(strings.TrimSpace(got)), []byte(h.token)) != 1 {
		httperror.Render(w, r, httperror.Unauthorized("a valid HR_DIRECTORY_TOKEN bearer token is required"))
		return
	}
	depts, err := h.depts.ListActive(r.Context())
	if !ok(w, r, err) {
		return
	}
	bundles, err := h.bundles.List(r.Context())
	if !ok(w, r, err) {
		return
	}
	out := make([]DirectoryGroup, 0, 2*len(depts)+len(bundles))
	seen := map[string]bool{}
	for _, d := range depts {
		out = append(out, DirectoryGroup{Name: d.GroupName, DisplayName: d.Name, Kind: "department"})
		seen[groupmatch.Normalize(d.GroupName)] = true
		if d.LeadGroup != "" {
			out = append(out, DirectoryGroup{Name: d.LeadGroup, DisplayName: d.Name + " (leads)", Kind: "department"})
			seen[groupmatch.Normalize(d.LeadGroup)] = true
		}
	}
	access := map[string]int{}
	for _, b := range bundles {
		for _, role := range b.Roles {
			key := groupmatch.Normalize(role.GroupName)
			if key == "" || seen[key] {
				continue
			}
			i, found := access[key]
			if !found {
				i = len(out)
				access[key] = i
				out = append(out, DirectoryGroup{Name: role.GroupName, Kind: "access"})
			}
			if !slices.Contains(out[i].AccessGroups, b.Name) {
				out[i].AccessGroups = append(out[i].AccessGroups, b.Name)
			}
		}
	}
	for _, i := range access {
		out[i].DisplayName = out[i].Name + " (" + strings.Join(out[i].AccessGroups, ", ") + ")"
	}
	writeJSON(w, http.StatusOK, map[string]any{"groups": out})
}
