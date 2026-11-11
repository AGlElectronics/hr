package http_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

	rec := get("Bearer " + directoryToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Items []hrhttp.DirectoryGroup `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{}
	for _, g := range out.Items {
		kinds[g.Name] = g.Kind
	}
	if kinds[group] != "department" || kinds[group+"-lead"] != "department_lead" {
		t.Fatalf("department %q and its lead group missing: %+v", group, out.Items)
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
