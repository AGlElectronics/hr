package http

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/rs/zerolog"

	"github.com/AGlElectronics/hr/backend/internal/config"
	"github.com/AGlElectronics/hr/backend/internal/denlink"
	"github.com/AGlElectronics/hr/backend/internal/provisioners"
	"github.com/AGlElectronics/hr/backend/migrations"
)

// HR_PORTAL_TEST_DATABASE_URL is a PostgreSQL the test may create a schema
// in. It must not be the tasks database. The schema is dropped afterwards.
const portalTestEnv = "HR_PORTAL_TEST_DATABASE_URL"

func TestPortalTodos(t *testing.T) {
	pool := portalTestPool(t)
	ctx := context.Background()
	cfg := &config.Config{
		AppEnv:   "development",
		AuthMode: config.AuthModeOIDC,
		Den:      denlink.Config{Slug: "hr", AppEnv: "development", Mode: denlink.ModeStandalone},
	}
	unpaired := denlink.New(pool, cfg.DenConfig("test"), zerolog.Nop())
	router := hrRouter(t, pool, cfg, unpaired)

	rec := callPortal(router, "", false)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unpaired: %d %s", rec.Code, rec.Body.String())
	}

	const cred = "dev-credential"
	if _, err := pool.Exec(ctx, `
		INSERT INTO den_link (id, den_base_url, credential, module_id, paired_at, bundle)
		VALUES (1, 'http://den.conservices.test', $1, 'mod-1', now(), '{"brand":{},"settings":{"data":{}}}')`, cred); err != nil {
		t.Fatalf("seed den_link: %v", err)
	}
	paired := denlink.New(pool, cfg.DenConfig("test"), zerolog.Nop())
	if err := paired.Restore(ctx); err != nil {
		t.Fatalf("restore: %v", err)
	}
	router = hrRouter(t, pool, cfg, paired)

	if rec := callPortal(router, "", true); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no credential: %d %s", rec.Code, rec.Body.String())
	}
	if rec := callPortal(router, "Bearer wrong", true); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong credential: %d %s", rec.Code, rec.Body.String())
	}
	if rec := callPortal(router, "Bearer "+cred, false); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing person: %d %s", rec.Code, rec.Body.String())
	}
	rec = callPortal(router, "Bearer "+cred, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("todos: %d %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Items []any `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Items == nil || len(body.Items) != 0 {
		t.Fatalf("items = %#v, want an empty list", body.Items)
	}

	if _, err := pool.Exec(ctx, `UPDATE den_link SET revoked_at = now()`); err != nil {
		t.Fatal(err)
	}
	revoked := denlink.New(pool, cfg.DenConfig("test"), zerolog.Nop())
	if err := revoked.Restore(ctx); err != nil {
		t.Fatal(err)
	}
	if rec := callPortal(hrRouter(t, pool, cfg, revoked), "Bearer "+cred, true); rec.Code != http.StatusUnauthorized {
		t.Fatalf("revoked: %d %s", rec.Code, rec.Body.String())
	}
}

func callPortal(router http.Handler, auth string, person bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/portal/todos", nil)
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	if person {
		req.Header.Set("X-Den-User-Issuer", "https://auth.conservices.test/realms/conservices")
		req.Header.Set("X-Den-User-Subject", "person-1")
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func hrRouter(t *testing.T, pool *pgxpool.Pool, cfg *config.Config, den *denlink.Client) http.Handler {
	t.Helper()
	return NewRouter(Deps{
		Config: cfg, Pool: pool, Logger: zerolog.Nop(), Den: den,
		IdP: provisioners.NewFakeIdP(zerolog.Nop()),
	}, "test")
}

func portalTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	base := os.Getenv(portalTestEnv)
	if base == "" {
		t.Skip(portalTestEnv + " not set")
	}
	ctx := context.Background()
	schema := "hr_portal_" + strings.ReplaceAll(uuid.NewString()[:13], "-", "")
	admin, err := pgxpool.New(ctx, base)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if _, err := admin.Exec(ctx, `CREATE EXTENSION IF NOT EXISTS pgcrypto WITH SCHEMA public`); err != nil {
		admin.Close()
		t.Fatalf("pgcrypto: %v", err)
	}
	if _, err := admin.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		admin.Close()
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `DROP SCHEMA `+schema+` CASCADE`)
		admin.Close()
	})
	dsn, err := withPortalSearchPath(base, schema)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	goose.SetBaseFS(migrations.FS)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpContext(ctx, db, "."); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func withPortalSearchPath(dsn, schema string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil || u.Scheme == "" {
		return "", fmt.Errorf("%s must be a postgres:// URL", portalTestEnv)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	return u.String(), nil
}
