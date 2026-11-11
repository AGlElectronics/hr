package auth

import (
	"errors"
	"net/http"
	"strings"

	"github.com/rs/zerolog/log"

	"github.com/AGlElectronics/hr/backend/internal/actor"
	"github.com/AGlElectronics/hr/backend/internal/authz"
	"github.com/AGlElectronics/hr/backend/internal/config"
	"github.com/AGlElectronics/hr/backend/internal/httperror"
)

// Middleware authenticates API requests and works out the caller's HR roles.
//   - AUTH_MODE=dev: X-Dev-User (or DEV_DEFAULT_USER) stamps the actor. The
//     caller is an HR admin unless X-Dev-Groups lists other groups.
//   - AUTH_MODE=oidc: requires a valid Bearer ID token from the shared Keycloak.
//
// resolver (may be nil) adds the caller's HR person record and the
// departments they lead.
func Middleware(cfg *config.Config, oidcVerifier *OIDCVerifier, resolver *authz.Resolver) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			var a *actor.Actor

			switch cfg.AuthMode {
			case config.AuthModeDev, "":
				name := strings.TrimSpace(r.Header.Get("X-Dev-User"))
				if name == "" {
					name = cfg.DevDefaultUser
				}
				if name == "" {
					name = "dev-hr-admin"
				}
				groups := cfg.AdminGroups
				if raw := strings.TrimSpace(r.Header.Get("X-Dev-Groups")); raw != "" {
					groups = nil
					for _, g := range strings.Split(raw, ",") {
						if g = strings.TrimSpace(g); g != "" {
							groups = append(groups, g)
						}
					}
				}
				a = &actor.Actor{Username: name, Issuer: "dev:", Subject: name, Groups: groups}

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
				a = &actor.Actor{Username: DisplayName(claims), Issuer: claims.Issuer, Subject: claims.Subject, Groups: claims.Groups}

			default:
				httperror.Render(w, r, httperror.Internal(errors.New("unknown auth mode")))
				return
			}

			a.Roles = RolesFor(cfg, a.Groups)
			a.HasAccess = len(a.Roles) > 0
			if a.HasAccess {
				if err := resolver.Resolve(ctx, a); err != nil {
					httperror.Render(w, r, httperror.Internal(err))
					return
				}
			}
			next.ServeHTTP(w, r.WithContext(actor.With(ctx, a)))
		})
	}
}

// RolesFor maps IdP groups to HR roles.
func RolesFor(cfg *config.Config, groups []string) []string {
	var roles []string
	for _, rg := range []struct {
		role   string
		groups []string
	}{{authz.RoleAdmin, cfg.AdminGroups}, {authz.RoleViewer, cfg.ViewerGroups}, {authz.RoleLead, cfg.LeadGroups}} {
		if inAnyGroup(groups, rg.groups) {
			roles = append(roles, rg.role)
		}
	}
	return roles
}

// RequireAccess rejects callers who are signed in but hold no HR role.
func RequireAccess(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a, ok := actor.FromContext(r.Context())
		if !ok || !a.HasAccess {
			httperror.Render(w, r, httperror.Forbidden("you don't have access to HR"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Require rejects callers who hold p neither globally nor as a department
// lead. Handlers check the department scope of lead permissions.
func Require(p authz.Permission) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			a, _ := actor.FromContext(r.Context())
			if !authz.Any(a, p) {
				httperror.Render(w, r, authz.Forbidden(p))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireGlobal rejects callers who don't hold p everywhere (admin-only
// routes such as settings and the Den connection).
func RequireGlobal(p authz.Permission) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			a, _ := actor.FromContext(r.Context())
			if !authz.Global(a, p) {
				httperror.Render(w, r, authz.Forbidden(p))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// inAnyGroup matches group names with or without Keycloak's leading "/".
func inAnyGroup(have, want []string) bool {
	for _, h := range have {
		h = strings.TrimPrefix(h, "/")
		for _, w := range want {
			if strings.EqualFold(h, strings.TrimPrefix(w, "/")) {
				return true
			}
		}
	}
	return false
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
