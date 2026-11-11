package http

import (
	"crypto/subtle"
	"net/http"
	"regexp"
	"strings"

	"github.com/AGlElectronics/hr/backend/internal/httperror"
	"github.com/AGlElectronics/hr/backend/internal/service"
)

// DirectoryGroup is one entry of GET /api/internal/directory/groups.
type DirectoryGroup struct {
	// Name is the IdP group (departments and their leads) or the access
	// group's slug (access groups exist only in HR).
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	// Kind is department, department_lead or access_group.
	Kind string `json:"kind"`
	// Groups are the IdP groups an access group grants (access groups only).
	Groups []string `json:"groups,omitempty"`
}

type directoryHandler struct {
	token   string
	depts   *service.DepartmentService
	bundles *service.BundleService
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(s string) string {
	return strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(s), "-"), "-")
}

// groups serves other modules (the wiki's share dialog) HR's departments,
// their lead groups and the access groups. It needs HR_DIRECTORY_TOKEN as
// a bearer token and is off (404) when that is not set.
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
	for _, d := range depts {
		out = append(out, DirectoryGroup{Name: d.GroupName, DisplayName: d.Name, Kind: "department"})
		if d.LeadGroup != "" {
			out = append(out, DirectoryGroup{Name: d.LeadGroup, DisplayName: d.Name + " (leads)", Kind: "department_lead"})
		}
	}
	for _, b := range bundles {
		g := DirectoryGroup{Name: slugify(b.Name), DisplayName: b.Name, Kind: "access_group", Groups: []string{}}
		for _, role := range b.Roles {
			if role.GroupName != "" {
				g.Groups = append(g.Groups, role.GroupName)
			}
		}
		out = append(out, g)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}
