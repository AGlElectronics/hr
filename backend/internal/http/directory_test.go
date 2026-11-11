package http_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AGlElectronics/hr/backend/internal/config"
	hrhttp "github.com/AGlElectronics/hr/backend/internal/http"
)

const directoryToken = "directory-test-token-0123456789"

// The wiki lists HR's departments, lead groups and access groups with
// HR_DIRECTORY_TOKEN; without it (or with a wrong one) nothing is shown.
func TestDirectoryGroups(t *testing.T) {
	router, pool := newTestRouter(t, func(_ *pgxpool.Pool, cfg *config.Config, _ *hrhttp.Deps) {
		cfg.DirectoryToken = directoryToken
	})
	do := clientAs(router, "test-hr-admin", "")
	_, group := createDepartment(t, do, pool, "Directory")
	// The role's IdP group comes from Stash's role catalog.
	bundleName, accessGroup := "Directory bundle "+uniqueNick("d"), "stash-volunteer"
	rec := do(http.MethodPost, "/api/access-bundles", map[string]any{
		"name": bundleName, "roles": []map[string]string{{"role_name": "stash:volunteer"}},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create bundle %d %s", rec.Code, rec.Body.String())
	}
	var bundle struct{ ID string }
	_ = json.Unmarshal(rec.Body.Bytes(), &bundle)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM access_bundles WHERE id = $1`, bundle.ID)
	})

	get := func(auth string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/internal/directory/groups", nil)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	for _, auth := range []string{"", "Bearer wrong-token-wrong-token", directoryToken} {
		if rec := get(auth); rec.Code != http.StatusUnauthorized {
			t.Fatalf("auth %q: want 401, got %d %s", auth, rec.Code, rec.Body.String())
		}
	}
	// A user session is not enough either.
	if rec := do(http.MethodGet, "/api/internal/directory/groups", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("dev user: want 401, got %d", rec.Code)
	}

	rec = get("Bearer " + directoryToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Groups []hrhttp.DirectoryGroup `json:"groups"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	byName := map[string]hrhttp.DirectoryGroup{}
	for _, g := range out.Groups {
		byName[g.Name] = g
	}
	if byName[group].Kind != "department" || byName[group+"-lead"].Kind != "department" {
		t.Fatalf("department %q and its lead group missing: %+v", group, out.Groups)
	}
	access := byName[accessGroup]
	if access.Kind != "access" || !slices.Contains(access.AccessGroups, bundleName) ||
		!strings.Contains(access.DisplayName, bundleName) {
		t.Fatalf("access group's IdP group %q missing: %+v", accessGroup, out.Groups)
	}
}

func TestDirectoryGroupsOff(t *testing.T) {
	router, _ := newTestRouter(t, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/internal/directory/groups", nil)
	req.Header.Set("Authorization", "Bearer "+directoryToken)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("without HR_DIRECTORY_TOKEN: want 404, got %d", rec.Code)
	}
}
