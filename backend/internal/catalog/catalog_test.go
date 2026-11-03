package catalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseSources(t *testing.T) {
	got := ParseSources(" stash=http://a/api/role-catalog , bad, events=http://b ,")
	if len(got) != 2 || got[0].Slug != "stash" || got[1].URL != "http://b" {
		t.Fatalf("unexpected %+v", got)
	}
}

func TestFetchAll(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"module":"stash","display_name":"Stash","roles":[{"key":"admin","group":"stash-admin","assignable":true}]}`))
	}))
	defer ok.Close()
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer bad.Close()

	f := &Fetcher{Sources: []Source{{Slug: "stash", URL: ok.URL}, {Slug: "events", URL: bad.URL}}}
	mods := f.FetchAll(context.Background())
	if len(mods) != 3 {
		t.Fatalf("want 3 modules (hr+2), got %d", len(mods))
	}
	if mods[0].Module != "hr" {
		t.Fatalf("first should be hr self, got %s", mods[0].Module)
	}
	if mods[1].Status != "ok" || len(mods[1].Roles) != 1 || mods[1].Roles[0].Group != "stash-admin" {
		t.Fatalf("stash: %+v", mods[1])
	}
	if mods[2].Status != "error" || mods[2].Module != "events" {
		t.Fatalf("events should error: %+v", mods[2])
	}
}
