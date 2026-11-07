// Package http builds the chi router and ties handlers to dependencies.
package http

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/hlog"

	"github.com/AGlElectronics/hr/backend/internal/actor"
	"github.com/AGlElectronics/hr/backend/internal/audit"
	"github.com/AGlElectronics/hr/backend/internal/auth"
	"github.com/AGlElectronics/hr/backend/internal/catalog"
	"github.com/AGlElectronics/hr/backend/internal/clientip"
	"github.com/AGlElectronics/hr/backend/internal/config"
	"github.com/AGlElectronics/hr/backend/internal/denlink"
	"github.com/AGlElectronics/hr/backend/internal/mail"
	"github.com/AGlElectronics/hr/backend/internal/provisioners"
	"github.com/AGlElectronics/hr/backend/internal/qualification"
	"github.com/AGlElectronics/hr/backend/internal/service"
)

// Deps groups the runtime dependencies the HTTP layer needs.
type Deps struct {
	Config       *config.Config
	Pool         *pgxpool.Pool
	Logger       zerolog.Logger
	OIDCVerifier *auth.OIDCVerifier

	IdP       provisioners.IdPProvisioner
	Workspace provisioners.WorkspaceProvisioner
	// Mailer sends HR's own notices; nil when SMTP is not configured.
	Mailer mail.Sender
	// Den is the pairing with Den; nil means a fresh standalone client.
	Den *denlink.Client
	// RoleGate is shared with the background worker; nil builds one from Config.
	RoleGate *service.RoleGateService
}

// NewRoleGate builds the training-requirement service from config. Gating
// is off (requirements not enforced) without LEARN_HOLDINGS_URL and token.
func NewRoleGate(cfg *config.Config, pool *pgxpool.Pool, idp provisioners.IdPProvisioner, logger zerolog.Logger) *service.RoleGateService {
	var holdings service.HoldingsSource
	if cfg.RoleGateConfigured() {
		holdings = &qualification.HoldingsClient{URL: cfg.LearnHoldingsURL, Token: cfg.LearnServiceToken}
	}
	accounts, _ := idp.(provisioners.AccountManager)
	quals := &qualification.Fetcher{Sources: catalog.ParseSources(cfg.QualificationCatalogSources)}
	return service.NewRoleGateService(pool, audit.NewWriter(pool, logger), accounts, holdings, quals, logger)
}

// NewRouter wires the chi router with middleware, CORS and routes.
func NewRouter(deps Deps, version string) http.Handler {
	r := chi.NewRouter()

	r.Use(chimiddleware.RequestID)
	r.Use(clientip.Middleware(deps.Config.TrustedProxyCIDRs))
	r.Use(chimiddleware.Recoverer)
	r.Use(chimiddleware.Timeout(60 * time.Second))

	r.Use(hlog.NewHandler(deps.Logger))
	r.Use(hlog.AccessHandler(func(req *http.Request, status, size int, duration time.Duration) {
		hlog.FromRequest(req).Info().
			Str("method", req.Method).
			Str("path", req.URL.Path).
			Int("status", status).
			Int("size", size).
			Dur("duration", duration).
			Msg("http")
	}))
	r.Use(hlog.RequestIDHandler("request_id", "X-Request-ID"))

	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   deps.Config.CORSAllowedOrigins,
		AllowedMethods:   []string{"GET", "POST", "PATCH", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Content-Type", "Authorization", "X-Dev-User", "X-Request-ID"},
		ExposedHeaders:   []string{"X-Request-ID"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	health := newHealthHandler(deps.Pool, version)
	r.Get("/livez", health.livez)
	r.Get("/readyz", health.readyz)

	fetcher := &catalog.Fetcher{Sources: catalog.ParseSources(deps.Config.RoleCatalogSources)}
	r.Get("/api/role-catalog", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, catalog.Self())
	})

	den := deps.Den
	if den == nil {
		den = denlink.New(deps.Pool, denlink.Config{Slug: "hr", Version: version, Mode: deps.Config.DenMode}, deps.Logger)
	}
	r.Get("/api/brand", den.BrandHandler("/api/brand"))
	r.Get("/api/brand/{role}", den.AssetHandler)
	r.Get("/api/den/state", den.StateHandler)

	auditWriter := audit.NewWriter(deps.Pool, deps.Logger)
	settingsSvc := service.NewSettingsService(deps.Pool, auditWriter, den)
	usernames, _ := deps.IdP.(provisioners.UsernameDirectory)
	peopleSvc := service.NewPeopleService(deps.Pool, auditWriter, settingsSvc, usernames)
	draftSvc := service.NewDraftService(deps.Pool, auditWriter)
	provSvc := service.NewProvisionService(deps.Pool, draftSvc, peopleSvc, deps.IdP, deps.Workspace, auditWriter, settingsSvc)
	checks := &checksHandler{people: peopleSvc, settings: settingsSvc, client: &http.Client{Timeout: 5 * time.Second}}
	notifySvc := service.NewNotifyService(deps.Logger, draftSvc, peopleSvc, auditWriter)
	groupEnsurer, _ := deps.IdP.(provisioners.GroupEnsurer)
	deptSvc := service.NewDepartmentService(deps.Pool, auditWriter, groupEnsurer)
	deptSvc.SetHooks(service.ParseOffboardHooks(deps.Config.DepartmentHooks), deps.Logger)
	onboardingSvc := service.NewOnboardingService(peopleSvc, draftSvc, deptSvc, func(ctx context.Context) []service.RolePlanInput {
		out := []service.RolePlanInput{}
		for _, r := range catalog.DefaultRoles(fetcher.FetchAll(ctx)) {
			scope := r.Scope
			if scope == "" {
				scope = "global"
			}
			out = append(out, service.RolePlanInput{RoleName: r.Key, GroupName: r.Group, Scope: scope})
		}
		return out
	})
	depts := &departmentHandler{svc: deptSvc}
	onboarding := &onboardingHandler{svc: onboardingSvc, prov: provSvc}

	gate := deps.RoleGate
	if gate == nil {
		gate = NewRoleGate(deps.Config, deps.Pool, deps.IdP, deps.Logger)
	}
	provSvc.SetRoleGate(gate)
	onboardingSvc.SetRoleGate(gate)
	roleGate := &roleGateHandler{svc: gate, quals: &qualification.Fetcher{Sources: catalog.ParseSources(deps.Config.QualificationCatalogSources)}}

	accounts, _ := deps.IdP.(provisioners.AccountManager)
	directory, _ := deps.IdP.(provisioners.Directory)
	lifecycleSvc := service.NewLifecycleService(deps.Pool, peopleSvc, draftSvc, deptSvc, accounts, deps.Mailer, settingsSvc, auditWriter)
	lifecycleSvc.SetRoleGate(gate)
	lifecycleSvc.SetOffboardHooks(service.ParseOffboardHooks(deps.Config.OffboardHooks))
	dashSvc := service.NewDashboardService(deps.Pool, peopleSvc, deptSvc)
	misc := &miscHandler{settings: settingsSvc, dash: dashSvc, directory: directory, depts: deptSvc}

	people := &peopleHandler{svc: peopleSvc, lifecycle: lifecycleSvc}
	drafts := &draftHandler{svc: draftSvc}
	prov := &provisionHandler{svc: provSvc}
	notify := &notifyHandler{svc: notifySvc}

	r.Route("/api", func(api chi.Router) {
		api.Use(den.RequirePaired)
		api.Use(auth.Middleware(deps.Config, deps.OIDCVerifier))

		api.Get("/me", meHandler(deps.Config))
		prefs := den.PreferencesHandler(func(r *http.Request) (string, string, bool) {
			a, _ := actor.FromContext(r.Context())
			if a == nil || a.Issuer == "" || a.Subject == "" {
				return "", "", false
			}
			return a.Issuer, a.Subject, true
		})
		api.Get("/me/preferences", prefs)
		api.Put("/me/preferences", prefs)

		api.Group(func(api chi.Router) {
			api.Use(auth.RequireAccess)
			api.Get("/access-catalog", func(w http.ResponseWriter, req *http.Request) {
				writeJSON(w, http.StatusOK, map[string]any{"modules": fetcher.FetchAll(req.Context())})
			})
			api.Get("/qualification-catalog", roleGate.qualificationCatalog)
			api.Get("/role-requirements", roleGate.listRequirements)
			api.Post("/role-requirements", roleGate.createRequirement)
			api.Delete("/role-requirements/{id}", roleGate.deleteRequirement)
			api.Get("/pending-grants", roleGate.listGrants)
			api.Post("/pending-grants/{id}/override", roleGate.override)

			api.Route("/people", func(pr chi.Router) {
				pr.Get("/", people.list)
				pr.Post("/", people.create)
				pr.Get("/{id}", people.get)
				pr.Put("/{id}", people.update)
				pr.Get("/{id}/access", people.access)
				pr.Put("/{id}/access", people.changeAccess)
				pr.Post("/{id}/status", people.setStatus)
				pr.Post("/{id}/offboard", people.offboard)
			})

			api.Get("/settings", misc.getSettings)
			api.Put("/settings", misc.updateSettings)
			api.Route("/den", func(dr chi.Router) {
				dr.Get("/", den.StatusHandler)
				dr.Post("/pair", den.PairHandler)
				dr.Post("/sync", den.SyncHandler)
				dr.Delete("/pair", den.UnpairHandler)
			})
			api.Get("/check/email", checks.email)
			api.Get("/check/username", checks.username)
			api.Get("/address/search", checks.addressSearch)
			api.Get("/dashboard", misc.dashboard)
			api.Get("/idp/groups", misc.idpGroups)

			api.Route("/departments", func(dr chi.Router) {
				dr.Get("/", depts.list)
				dr.Post("/", depts.create)
				dr.Get("/{id}", depts.get)
				dr.Put("/{id}", depts.update)
			})

			api.Route("/onboardings", func(or chi.Router) {
				or.Post("/", onboarding.start)
				or.Get("/{id}", onboarding.get)
				or.Put("/{id}/details", onboarding.updateDetails)
				or.Post("/{id}/complete", onboarding.complete)
			})

			api.Route("/drafts", func(dr chi.Router) {
				dr.Get("/", drafts.list)
				dr.Post("/", drafts.create)
				dr.Get("/{id}", drafts.get)
				dr.Put("/{id}/roles", drafts.assignRoles)
				dr.Get("/{id}/provisioning-jobs", prov.listJobs)
				dr.Post("/{id}/notify", notify.send)
			})
		})
	})

	return r
}

func meHandler(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a, _ := actor.FromContext(r.Context())
		out := map[string]any{"username": "", "auth_mode": cfg.AuthMode, "has_access": false, "groups": []string{}}
		if a != nil {
			out["username"], out["has_access"] = a.Username, a.HasAccess
			if a.Groups != nil {
				out["groups"] = a.Groups
			}
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(out)
	}
}
