// Package devuser extracts the acting user from the trivial X-Dev-User
// request header. This is the ONLY auth mechanism in the HR scaffold —
// operator login via Keycloak/OIDC is deferred (see ADR-0003). It must
// never be used outside local/dev deployments.
package devuser

import (
	"context"
	"net/http"
	"strings"
)

type ctxKey int

const userKey ctxKey = 1

// User is the trivial dev-mode actor stamped onto writes (audit, draft
// authorship, etc.). It is not an authenticated identity.
type User struct {
	Username string
}

// Middleware attaches a *User to the request context. If the header is
// missing or empty, defaultUser is used so local workflows never trip over
// an auth-less request.
func Middleware(defaultUser string) func(http.Handler) http.Handler {
	if defaultUser == "" {
		defaultUser = "dev-hr-admin"
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			name := strings.TrimSpace(r.Header.Get("X-Dev-User"))
			if name == "" {
				name = defaultUser
			}
			ctx := context.WithValue(r.Context(), userKey, &User{Username: name})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// FromContext returns the dev user attached by Middleware. The second return
// value is false only when the middleware did not run (e.g. tests calling
// handlers directly).
func FromContext(ctx context.Context) (*User, bool) {
	u, ok := ctx.Value(userKey).(*User)
	if !ok || u == nil {
		return nil, false
	}
	return u, true
}
