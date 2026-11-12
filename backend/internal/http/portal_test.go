package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/AGlElectronics/hr/backend/internal/config"
	"github.com/AGlElectronics/hr/backend/internal/denlink"
	"github.com/AGlElectronics/hr/backend/internal/provisioners"
	"github.com/AGlElectronics/hr/backend/internal/testdb"
)

func TestPortalTodos(t *testing.T) {
	// Pairing lives in den_link, a singleton. A private schema keeps that
	// row out of the database the other tests share.
	pool := testdb.OpenSchema(t)
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
		IDP: provisioners.NewFakeIDP(zerolog.Nop()),
	}, "test")
}
