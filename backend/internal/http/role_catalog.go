package http

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/AGlElectronics/hr/backend/internal/actor"
	"github.com/AGlElectronics/hr/backend/internal/authbase"
	"github.com/AGlElectronics/hr/backend/internal/catalog"
)

// roleCatalogAuth lets HR_SERVICE_TOKEN through (constant-time compare) and
// otherwise needs a staff sign-in, any role or none (role catalog contract).
func roleCatalogAuth(hrToken string, authMW func(http.Handler) http.Handler, next http.Handler) http.HandlerFunc {
	signedIn := authMW(next)
	return func(w http.ResponseWriter, r *http.Request) {
		if hrToken != "" {
			got := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
			if subtle.ConstantTimeCompare([]byte(got), []byte(hrToken)) == 1 {
				next.ServeHTTP(w, r)
				return
			}
		}
		signedIn.ServeHTTP(w, r)
	}
}

// roleCatalogHandler serves HR's own catalog with its revision as the ETag.
func roleCatalogHandler(self catalog.Module) http.HandlerFunc {
	etag := `"` + self.Revision + `"`
	body := self.Body()
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", etag)
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		writeJSON(w, http.StatusOK, body)
	}
}

// userKey is the signed-in person's (issuer, subject), for rate limits.
func userKey(r *http.Request) (authbase.UserKey, bool) {
	a, ok := actor.FromContext(r.Context())
	if !ok || a == nil || a.Subject == "" {
		return authbase.UserKey{}, false
	}
	return authbase.UserKey{Issuer: a.Issuer, Subject: a.Subject}, true
}
