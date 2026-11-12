package catalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestParseSources(t *testing.T) {
	got := ParseSources(" stash=http://a/api/role-catalog , bad, events=http://b ,")
	if len(got) != 2 || got[0].Slug != "stash" || got[1].URL != "http://b" {
		t.Fatalf("unexpected %+v", got)
	}
}

func TestFetchSendsTokenAndHonoursETag(t *testing.T) {
	var hits, notModified atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("t", 32) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("ETag", `"rev-1"`)
		if r.Header.Get("If-None-Match") == `"rev-1"` {
			notModified.Add(1)
			w.WriteHeader(http.StatusNotModified)
			return
		}
		_, _ = w.Write([]byte(`{"revision":"rev-1","roles":[{"key":"mailbox:fundraising","group":"desk/mailboxes/fundraising",
			"group_display":"/desk/mailboxes/Fundraising","assignable":true,"privileged":false,"dynamic":true}]}`))
	}))
	defer srv.Close()

	cache := &memCache{m: map[string]Module{}}
	f := &Fetcher{Sources: []Source{{Slug: "desk", URL: srv.URL}}, Cache: cache, Token: strings.Repeat("t", 32)}
	mods := f.FetchAll(context.Background())
	desk := mods[1]
	if desk.Status != StatusOK || desk.Revision != "rev-1" || desk.ETag != `"rev-1"` {
		t.Fatalf("first fetch: %+v", desk)
	}
	r := desk.Roles[0]
	if !r.Dynamic || r.Group != "desk/mailboxes/fundraising" || r.IDPGroup() != "/desk/mailboxes/Fundraising" {
		t.Fatalf("role: %+v", r)
	}
	mods = f.FetchAll(context.Background())
	if notModified.Load() != 1 || mods[1].Status != StatusOK || len(mods[1].Roles) != 1 {
		t.Fatalf("second fetch should reuse the cache on 304: %d %+v", notModified.Load(), mods[1])
	}

	f.Token = ""
	f.Invalidate()
	mods = f.FetchAll(context.Background())
	if mods[1].Status != StatusCached || !strings.Contains(mods[1].Error, "HR_SERVICE_TOKEN") {
		t.Fatalf("without the token the module refuses and the cache is shown: %+v", mods[1])
	}
}

func TestSourcesMergeOverride(t *testing.T) {
	f := &Fetcher{
		Sources: []Source{{Slug: "stash", URL: "http://override/api/role-catalog"}, {Slug: "extra", URL: "http://extra"}},
		Discover: func(context.Context) []Source {
			return []Source{{Slug: "stash", URL: "http://den/stash"}, {Slug: "wiki", URL: "http://den/wiki"}}
		},
	}
	got := f.sources(context.Background())
	if len(got) != 3 || got[0].URL != "http://override/api/role-catalog" || got[1].Slug != "wiki" || got[2].Slug != "extra" {
		t.Fatalf("merge: %+v", got)
	}
	f.Discover = nil
	if only := f.sources(context.Background()); len(only) != 2 || only[0].Slug != "stash" {
		t.Fatalf("sources alone: %+v", only)
	}
}

func TestSelfContract(t *testing.T) {
	m := Self("/HR-Admin", "hr-viewer", "hr-lead")
	if m.Revision == "" || m.Revision != Self("/HR-Admin", "hr-viewer", "hr-lead").Revision {
		t.Fatalf("revision must be stable: %q", m.Revision)
	}
	for _, r := range m.Roles {
		if !r.Privileged || r.Dynamic {
			t.Fatalf("every HR role is privileged and static: %+v", r)
		}
	}
	if a := m.Roles[0]; a.Group != "hr-admin" || a.GroupDisplay != "/HR-Admin" {
		t.Fatalf("admin group forms: %+v", a)
	}
	if Self("hr-admin", "hr-viewer", "x").Revision == m.Revision {
		t.Fatal("revision must change with the roles")
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
