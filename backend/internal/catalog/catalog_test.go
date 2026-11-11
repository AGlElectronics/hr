package catalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestParseSources(t *testing.T) {
	got := ParseSources(" stash=http://a/api/role-catalog , bad, events=http://b ,")
	if len(got) != 2 || got[0].Slug != "stash" || got[1].URL != "http://b" {
		t.Fatalf("unexpected %+v", got)
	}
}

func TestParseDenModules(t *testing.T) {
	got := ParseDenModules([]byte(`[{"slug":"hr","internal_url":"http://hr"},
		{"slug":"stash","internal_url":"https://stash-backend.stash.svc:8080/"},
		{"slug":"wiki","role_catalog_url":"http://wiki/api/role-catalog"},{"slug":"empty"}]`))
	if len(got) != 2 || got[0].URL != "https://stash-backend.stash.svc:8080/api/role-catalog" || got[1].Slug != "wiki" {
		t.Fatalf("unexpected %+v", got)
	}
}

type memCache struct{ m map[string]Module }

func (c *memCache) Load(context.Context) (map[string]Module, error) { return c.m, nil }
func (c *memCache) Save(_ context.Context, m Module) error          { c.m[m.Module] = m; return nil }

func TestFetchAllCachesAndFallsBack(t *testing.T) {
	var down atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if down.Load() {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(`{"module":"liar","display_name":"Stash","roles":[{"key":"admin","group":"stash-admin","assignable":true}]}`))
	}))
	defer srv.Close()
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer bad.Close()

	cache := &memCache{m: map[string]Module{}}
	f := &Fetcher{Sources: []Source{{Slug: "stash", URL: srv.URL}, {Slug: "events", URL: bad.URL}}, Cache: cache, Self: Self("hr-admin", "hr-viewer", "hr-lead")}
	mods := f.FetchAll(context.Background())
	if len(mods) != 3 || mods[0].Module != "hr" {
		t.Fatalf("want hr + 2 modules, got %+v", mods)
	}
	if mods[1].Module != "stash" || mods[1].Status != StatusOK || mods[1].Roles[0].Group != "stash-admin" {
		t.Fatalf("stash (module name comes from the source, not the payload): %+v", mods[1])
	}
	if mods[2].Status != StatusError {
		t.Fatalf("events should error: %+v", mods[2])
	}

	down.Store(true)
	mods = f.FetchAll(context.Background())
	if mods[1].Status != StatusCached || len(mods[1].Roles) != 1 || mods[1].Error == "" {
		t.Fatalf("offline stash should come from the cache: %+v", mods[1])
	}
}

func TestIndex(t *testing.T) {
	ix := NewIndex([]Module{
		{Module: "stash", Status: StatusOK, Roles: []Role{{Key: "volunteer", Group: "stash-volunteer"}, {Key: "admin", Group: "stash-admin"}}},
		{Module: "events", Status: StatusError},
	})
	if r, s := ix.Lookup("stash:volunteer"); s != RoleKnown || r.Group != "stash-volunteer" {
		t.Fatalf("known role: %v %v", r, s)
	}
	if r, _ := ix.Lookup("stash:admin"); !r.AdminType() {
		t.Fatal("stash:admin should be admin-type")
	}
	if _, s := ix.Lookup("stash:scanner"); s != RoleRetired {
		t.Fatalf("missing role of a loaded module should be retired, got %v", s)
	}
	if _, s := ix.Lookup("events:x"); s != RoleUnknownModule {
		t.Fatalf("role of an unloaded module: %v", s)
	}
}
