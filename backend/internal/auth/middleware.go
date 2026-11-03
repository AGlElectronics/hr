package auth

import (
	"errors"
	"net/http"
	"strings"

	"github.com/rs/zerolog/log"

	"github.com/AGlElectronics/hr/backend/internal/actor"
	"github.com/AGlElectronics/hr/backend/internal/config"
	"github.com/AGlElectronics/hr/backend/internal/httperror"
)

// Middleware authenticates API requests.
//   - AUTH_MODE=dev: X-Dev-User (or DEV_DEFAULT_USER) stamps the actor; no gate.
//   - AUTH_MODE=oidc: requires a valid Bearer ID token from the shared Keycloak.
func Middleware(cfg *config.Config, oidcVerifier *OIDCVerifier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()

			switch cfg.AuthMode {
			case config.AuthModeDev, "":
				name := strings.TrimSpace(r.Header.Get("X-Dev-User"))
				if name == "" {
					name = cfg.DevDefaultUser
				}
				if name == "" {
					name = "dev-hr-admin"
				}
				ctx = actor.With(ctx, &actor.Actor{Username: name})

			case config.AuthModeOIDC:
				if oidcVerifier == nil {
					httperror.Render(w, r, httperror.Internal(errors.New("oidc verifier not initialised")))
					return
				}
				bearer := bearerFromHeader(r.Header.Get("Authorization"))
				if bearer == "" {
					httperror.Render(w, r, httperror.Unauthorized("missing bearer token"))
					return
				}
				claims, err := oidcVerifier.Verify(ctx, bearer)
				if err != nil {
					log.Ctx(ctx).Debug().Err(err).Msg("hr oidc verify failed")
					httperror.Render(w, r, httperror.Unauthorized("invalid token"))
					return
				}
				ctx = actor.With(ctx, &actor.Actor{Username: DisplayName(claims)})

			default:
				httperror.Render(w, r, httperror.Internal(errors.New("unknown auth mode")))
				return
			}

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func bearerFromHeader(h string) string {
	if h == "" {
		return ""
	}
	parts := strings.SplitN(h, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}
