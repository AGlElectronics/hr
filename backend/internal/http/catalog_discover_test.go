package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/AGlElectronics/hr/backend/internal/catalog"
	"github.com/AGlElectronics/hr/backend/internal/config"
	"github.com/AGlElectronics/hr/backend/internal/denlink"
	"github.com/AGlElectronics/hr/backend/internal/testdb"
)

func TestDiscoverIgnoresNotPaired(t *testing.T) {
	var buf bytes.Buffer
	log := zerolog.New(&buf)
	den := denlink.New(nil, denlink.Config{Slug: "hr", AppEnv: "development"}, zerolog.Nop())
	if got := discoverRoleCatalogs(context.Background(), den, log); got != nil {
		t.Fatalf("standalone: %+v", got)
	}
	if buf.Len() != 0 {
		t.Fatalf("ErrNotPaired was logged: %s", buf.String())
	}
}

// TestDiscoverRoleCatalogs points a paired denlink client at an httptest
// Den (GET /api/v1/integrations/modules). Empty slugs and catalog URLs are
// skipped, ROLE_CATALOG_SOURCES replaces a URL and adds a module, and a
// failed call is logged without dropping the override list. The access
// catalog route uses the same hook.
func TestDiscoverRoleCatalogs(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	restoreDenLink(t, pool)
	restoreCatalogCache(t, pool, "desk", "learn", "stash")

	const credential = "module-cred"
	desk := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("Den's desk URL was fetched; ROLE_CATALOG_SOURCES should replace it")
	}))
	t.Cleanup(desk.Close)
	var overrideHits, learnHits, extraHits atomic.Int32
	catalogOf := func(slug string, hits *atomic.Int32) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			_, _ = w.Write([]byte(`{"module":"` + slug + `","roles":[{"key":"volunteer","group":"` + slug + `-volunteer","assignable":true}]}`))
		}
	}
	override := httptest.NewServer(catalogOf("desk", &overrideHits))
	t.Cleanup(override.Close)
	learn := httptest.NewServer(catalogOf("learn", &learnHits))
	t.Cleanup(learn.Close)
	extra := httptest.NewServer(catalogOf("stash", &extraHits))
	t.Cleanup(extra.Close)

	var denHits atomic.Int32
	var mode atomic.Value
	mode.Store("ok")
	denSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		denHits.Add(1)
		if r.URL.Path != "/api/v1/integrations/modules" {
			t.Errorf("path %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+credential {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"invalid or revoked credential"}}`))
			return
		}
		if mode.Load().(string) == "down" {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"error":{"message":"unavailable"}}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"modules": []map[string]string{
			{"slug": "desk", "display_name": "Desk", "base_url": "https://desk.example", "role_catalog_url": desk.URL},
			{"slug": "wiki", "display_name": "Wiki", "role_catalog_url": ""},
			{"slug": "", "role_catalog_url": "http://ignored.example/api/role-catalog"},
			{"slug": "learn", "display_name": "Learn", "role_catalog_url": learn.URL},
		}})
	}))
	t.Cleanup(denSrv.Close)

	if _, err := pool.Exec(ctx, `
		INSERT INTO den_link (id, den_base_url, credential, module_id)
		VALUES (1, $1, $2, 'hr')`, denSrv.URL, credential); err != nil {
		t.Fatal(err)
	}
	newClient := func() *denlink.Client {
		return denlink.New(pool, denlink.Config{Slug: "hr", AppEnv: "development"}, zerolog.Nop())
	}
	client := newClient()

	var buf bytes.Buffer
	log := zerolog.New(&buf)
	got := discoverRoleCatalogs(ctx, client, log)
	if len(got) != 2 || got[0].Slug != "desk" || got[0].URL != desk.URL || got[1].Slug != "learn" || got[1].URL != learn.URL {
		t.Fatalf("sources: %+v", got)
	}
	if denHits.Load() != 1 || buf.Len() != 0 {
		t.Fatalf("hits %d log %s", denHits.Load(), buf.String())
	}

	f := &catalog.Fetcher{
		Sources: catalog.ParseSources("desk=" + override.URL + ",stash=" + extra.URL),
		Discover: func(ctx context.Context) []catalog.Source {
			return discoverRoleCatalogs(ctx, client, log)
		},
		Self: catalog.Self("hr-admin", "hr-viewer", "hr-lead"),
	}
	mods := f.FetchAll(ctx)
	if len(mods) != 4 || mods[0].Module != "hr" || mods[1].Module != "desk" || mods[1].Source != override.URL || mods[1].Status != catalog.StatusOK ||
		mods[2].Module != "learn" || mods[2].Status != catalog.StatusOK || mods[3].Module != "stash" || mods[3].Status != catalog.StatusOK {
		t.Fatalf("merged catalogs: %+v", mods)
	}
	if overrideHits.Load() != 1 || learnHits.Load() != 1 || extraHits.Load() != 1 || denHits.Load() != 1 {
		t.Fatalf("hits override %d learn %d extra %d den %d", overrideHits.Load(), learnHits.Load(), extraHits.Load(), denHits.Load())
	}

	// A new client has no cache, so it calls Den again and sees the failure.
	mode.Store("down")
	buf.Reset()
	if again := discoverRoleCatalogs(ctx, newClient(), log); again != nil {
		t.Fatalf("Den down: %+v", again)
	}
	if !strings.Contains(buf.String(), "paired modules") || denHits.Load() != 2 {
		t.Fatalf("hits %d log %s", denHits.Load(), buf.String())
	}
	f.Discover = func(ctx context.Context) []catalog.Source {
		return discoverRoleCatalogs(ctx, newClient(), log)
	}
	mods = f.FetchAll(ctx)
	if len(mods) != 3 || mods[1].Module != "desk" || mods[1].Source != override.URL || mods[2].Module != "stash" {
		t.Fatalf("override list while Den is down: %+v", mods)
	}

	// The router uses the client that still holds Den's successful list.
	cfg := &config.Config{
		AppEnv:             "development",
		AuthMode:           config.AuthModeDev,
		AdminGroups:        []string{"hr-admin"},
		ViewerGroups:       []string{"hr-viewer"},
		LeadGroups:         []string{"hr-lead"},
		RoleCatalogSources: "desk=" + override.URL + ",stash=" + extra.URL,
	}
	router := NewRouter(Deps{Config: cfg, Pool: pool, Logger: zerolog.Nop(), Den: client}, "test")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/access-catalog", nil)
	req.Header.Set("X-Dev-User", "test-hr-admin")
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("access catalog %d %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Modules []catalog.Module `json:"modules"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Modules) != 4 || body.Modules[1].Module != "desk" || body.Modules[1].Source != override.URL || body.Modules[2].Module != "learn" {
		t.Fatalf("router catalog: %+v", body.Modules)
	}
}

func restoreDenLink(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	var (
		base, cred, moduleID, lastError string
		paired, synced, revoked         *string
		bundle                          []byte
	)
	err := pool.QueryRow(ctx, `
		SELECT den_base_url, credential, module_id, paired_at::text, synced_at::text, last_error, bundle, revoked_at::text
		  FROM den_link WHERE id = 1`).Scan(&base, &cred, &moduleID, &paired, &synced, &lastError, &bundle, &revoked)
	saved := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM den_link WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cctx := context.Background()
		_, _ = pool.Exec(cctx, `DELETE FROM den_link WHERE id = 1`)
		if !saved {
			return
		}
		if _, err := pool.Exec(cctx, `
			INSERT INTO den_link (id, den_base_url, credential, module_id, paired_at, synced_at, last_error, bundle, revoked_at)
			VALUES (1, $1, $2, $3, $4::timestamptz, $5::timestamptz, $6, $7, $8::timestamptz)`,
			base, cred, moduleID, paired, synced, lastError, bundle, revoked); err != nil {
			t.Errorf("restore den_link: %v", err)
		}
	})
}

func restoreCatalogCache(t *testing.T, pool *pgxpool.Pool, slugs ...string) {
	t.Helper()
	ctx := context.Background()
	rows, err := pool.Query(ctx, `SELECT module, payload, source, fetched_at FROM catalog_cache WHERE module = ANY($1)`, slugs)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type saved struct {
		module, source string
		raw            []byte
		at             time.Time
	}
	var prev []saved
	for rows.Next() {
		var s saved
		if err := rows.Scan(&s.module, &s.raw, &s.source, &s.at); err != nil {
			t.Fatal(err)
		}
		prev = append(prev, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cctx := context.Background()
		if _, err := pool.Exec(cctx, `DELETE FROM catalog_cache WHERE module = ANY($1)`, slugs); err != nil {
			t.Errorf("clear catalog cache: %v", err)
			return
		}
		for _, s := range prev {
			if _, err := pool.Exec(cctx, `
				INSERT INTO catalog_cache (module, payload, source, fetched_at) VALUES ($1, $2, $3, $4)`,
				s.module, s.raw, s.source, s.at); err != nil {
				t.Errorf("restore catalog cache %s: %v", s.module, err)
			}
		}
	})
}
