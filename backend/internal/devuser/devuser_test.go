package devuser

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMiddleware_UsesHeaderWhenPresent(t *testing.T) {
	var seen *User
	h := Middleware("fallback")(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		u, _ := FromContext(r.Context())
		seen = u
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Dev-User", "alice")
	h.ServeHTTP(httptest.NewRecorder(), req)

	if seen == nil || seen.Username != "alice" {
		t.Fatalf("want username=alice, got %+v", seen)
	}
}

func TestMiddleware_UsesFallbackWhenMissing(t *testing.T) {
	var seen *User
	h := Middleware("fallback")(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		u, _ := FromContext(r.Context())
		seen = u
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	if seen == nil || seen.Username != "fallback" {
		t.Fatalf("want username=fallback, got %+v", seen)
	}
}

func TestFromContext_EmptyWhenMiddlewareDidNotRun(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if _, ok := FromContext(req.Context()); ok {
		t.Fatal("expected FromContext to return ok=false")
	}
}
