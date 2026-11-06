package qualification

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AGlElectronics/hr/backend/internal/catalog"
)

func TestHoldings(t *testing.T) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		subjects := r.URL.Query().Get("subjects")
		calls = append(calls, subjects)
		var parts []string
		for _, s := range strings.Split(subjects, ",") {
			if s == "kc-1" {
				parts = append(parts, `"kc-1":[{"key":"first-aid","expires_at":"2028-01-01T00:00:00Z"},{"key":"forklift","expires_at":null}]`)
			}
		}
		_, _ = w.Write([]byte(`{"holdings":{` + strings.Join(parts, ",") + `}}`))
	}))
	defer srv.Close()

	c := &HoldingsClient{URL: srv.URL + "/api/internal/holdings", Token: "secret", BatchSize: 2}
	got, err := c.Holdings(context.Background(), []string{"kc-1", "kc-2", "kc-3"})
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || calls[0] != "kc-1,kc-2" || calls[1] != "kc-3" {
		t.Fatalf("batches %v", calls)
	}
	held := HeldKeys(got["kc-1"])
	if len(got["kc-1"]) != 2 || !held["first-aid"] || !held["forklift"] || got["kc-1"][0].ExpiresAt == nil || got["kc-1"][1].ExpiresAt != nil {
		t.Fatalf("kc-1 holdings %+v", got["kc-1"])
	}
	if _, ok := got["kc-2"]; ok {
		t.Fatalf("unknown subject should be absent: %+v", got)
	}

	empty, err := c.Holdings(context.Background(), nil)
	if err != nil || len(empty) != 0 || len(calls) != 2 {
		t.Fatalf("no subjects should not call Learn: %v %v", empty, err)
	}

	bad := &HoldingsClient{URL: srv.URL, Token: "wrong"}
	if _, err := bad.Holdings(context.Background(), []string{"kc-1"}); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("want 401 error, got %v", err)
	}

	down := &HoldingsClient{URL: "http://127.0.0.1:1/holdings", Token: "secret"}
	if _, err := down.Holdings(context.Background(), []string{"kc-1"}); err == nil {
		t.Fatal("unreachable Learn should fail")
	}
}

func TestFetchAll(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"module":"learn","display_name":"Learn","qualifications":[{"key":"first-aid","name":"First aid","valid_months":24}]}`))
	}))
	defer ok.Close()

	f := &Fetcher{Sources: []catalog.Source{{Slug: "learn", URL: ok.URL}, {Slug: "other", URL: "http://127.0.0.1:1/x"}}}
	mods := f.FetchAll(context.Background())
	if len(mods) != 2 || mods[0].Status != "ok" || len(mods[0].Qualifications) != 1 || *mods[0].Qualifications[0].ValidMonths != 24 {
		t.Fatalf("learn: %+v", mods)
	}
	if mods[1].Status != "error" || mods[1].Module != "other" || mods[1].Qualifications == nil {
		t.Fatalf("other should error: %+v", mods[1])
	}
	if n := Names(mods); n["first-aid"] != "First aid" {
		t.Fatalf("names %v", n)
	}
}
