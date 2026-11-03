// Package http builds the chi router and ties handlers to dependencies.
package http

import (
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
	"github.com/AGlElectronics/hr/backend/internal/config"
	"github.com/AGlElectronics/hr/backend/internal/provisioners"
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
}

// NewRouter wires the chi router with middleware, CORS and routes.
func NewRouter(deps Deps, version string) http.Handler {
	r := chi.NewRouter()

	r.Use(chimiddleware.RequestID)
	r.Use(chimiddleware.RealIP)
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

	auditWriter := audit.NewWriter(deps.Pool, deps.Logger)
	peopleSvc := service.NewPeopleService(deps.Pool, auditWriter)
	draftSvc := service.NewDraftService(deps.Pool, auditWriter)
	provSvc := service.NewProvisionService(deps.Pool, draftSvc, peopleSvc, deps.IdP, deps.Workspace, auditWriter)
	apprSvc := service.NewApprovalService(deps.Pool, draftSvc, auditWriter)
	notifySvc := service.NewNotifyService(deps.Logger, draftSvc, peopleSvc, auditWriter)
	groupEnsurer, _ := deps.IdP.(provisioners.GroupEnsurer)
	deptSvc := service.NewDepartmentService(deps.Pool, auditWriter, groupEnsurer)
	onboardingSvc := service.NewOnboardingService(peopleSvc, draftSvc, deptSvc)
	depts := &departmentHandler{svc: deptSvc}
	onboarding := &onboardingHandler{svc: onboardingSvc, prov: provSvc}

	accounts, _ := deps.IdP.(provisioners.AccountManager)
	directory, _ := deps.IdP.(provisioners.Directory)
	lifecycleSvc := service.NewLifecycleService(deps.Pool, peopleSvc, draftSvc, deptSvc, accounts, auditWriter)
	settingsSvc := service.NewSettingsService(deps.Pool, auditWriter)
	dashSvc := service.NewDashboardService(deps.Pool, peopleSvc, deptSvc)
	misc := &miscHandler{settings: settingsSvc, dash: dashSvc, directory: directory, depts: deptSvc}

	people := &peopleHandler{svc: peopleSvc, lifecycle: lifecycleSvc}
	drafts := &draftHandler{svc: draftSvc}
	prov := &provisionHandler{svc: provSvc}
	appr := &approvalHandler{svc: apprSvc}
	notify := &notifyHandler{svc: notifySvc}

	r.Route("/api", func(api chi.Router) {
		api.Use(auth.Middleware(deps.Config, deps.OIDCVerifier))

		api.Get("/me", meHandler(deps.Config))

		api.Group(func(api chi.Router) {
			api.Use(auth.RequireAccess)
			api.Get("/access-catalog", func(w http.ResponseWriter, req *http.Request) {
				writeJSON(w, http.StatusOK, map[string]any{"modules": fetcher.FetchAll(req.Context())})
			})

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
				dr.Post("/{id}/provision", prov.trigger)
				dr.Get("/{id}/provisioning-jobs", prov.listJobs)
				dr.Post("/{id}/approvals", appr.decide)
				dr.Get("/{id}/approvals", appr.list)
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
