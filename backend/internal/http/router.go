// Package http builds the chi router and ties handlers to dependencies.
package http

import (
	"context"
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
	"github.com/AGlElectronics/hr/backend/internal/authz"
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

	// IdP is nil when IDP_PROVISIONER_MODE=off.
	IdP provisioners.IdPProvisioner
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

func firstOf(ss []string) string {
	if len(ss) == 0 {
		return ""
	}
	return ss[0]
}

// NewRouter wires the chi router with middleware, CORS and routes.
func NewRouter(deps Deps, version string) http.Handler {
	r := chi.NewRouter()
	cfg := deps.Config

	r.Use(chimiddleware.RequestID)
	r.Use(clientip.Middleware(cfg.TrustedProxyCIDRs))
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
		AllowedOrigins:   cfg.CORSAllowedOrigins,
		AllowedMethods:   []string{"GET", "POST", "PATCH", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Content-Type", "Authorization", "X-Dev-User", "X-Request-ID"},
		ExposedHeaders:   []string{"X-Request-ID"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	health := newHealthHandler(deps.Pool, version)
	r.Get("/livez", health.livez)
	r.Get("/readyz", health.readyz)

	self := catalog.Self(firstOf(cfg.AdminGroups), firstOf(cfg.ViewerGroups), firstOf(cfg.LeadGroups))
	fetcher := &catalog.Fetcher{Sources: catalog.ParseSources(cfg.RoleCatalogSources), Self: self}
	r.Get("/api/role-catalog", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, self)
	})

	den := deps.Den
	if den == nil {
		den = denlink.New(deps.Pool, denlink.Config{Slug: "hr", Version: version, Mode: cfg.DenMode}, deps.Logger)
	}
	r.Get("/api/brand", den.BrandHandler("/api/brand"))
	r.Get("/api/brand/{role}", den.AssetHandler)
	r.Get("/api/den/state", den.StateHandler)

	auditWriter := audit.NewWriter(deps.Pool, deps.Logger)
	chk := authz.NewChecker(deps.Pool)
	settingsSvc := service.NewSettingsService(deps.Pool, auditWriter, den)
	usernames, _ := deps.IdP.(provisioners.UsernameDirectory)
	peopleSvc := service.NewPeopleService(deps.Pool, auditWriter, settingsSvc, usernames)
	draftSvc := service.NewDraftService(deps.Pool, auditWriter)
	provSvc := service.NewProvisionService(deps.Pool, draftSvc, peopleSvc, deps.IdP, auditWriter, settingsSvc)
	checks := &checksHandler{people: peopleSvc, settings: settingsSvc, client: &http.Client{Timeout: 5 * time.Second}}
	groupEnsurer, _ := deps.IdP.(provisioners.GroupEnsurer)
	accounts, _ := deps.IdP.(provisioners.AccountManager)
	deptSvc := service.NewDepartmentService(deps.Pool, auditWriter, groupEnsurer, accounts, firstOf(cfg.LeadGroups))
	deptSvc.SetHooks(service.ParseOffboardHooks(cfg.DepartmentHooks), deps.Logger)
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
	onboarding := &onboardingHandler{svc: onboardingSvc, prov: provSvc, chk: chk}

	gate := deps.RoleGate
	if gate == nil {
		gate = NewRoleGate(cfg, deps.Pool, deps.IdP, deps.Logger)
	}
	provSvc.SetRoleGate(gate)
	onboardingSvc.SetRoleGate(gate)
	roleGate := &roleGateHandler{svc: gate, quals: &qualification.Fetcher{Sources: catalog.ParseSources(cfg.QualificationCatalogSources)}, chk: chk}

	directory, _ := deps.IdP.(provisioners.Directory)
	lifecycleSvc := service.NewLifecycleService(deps.Pool, peopleSvc, draftSvc, deptSvc, accounts, deps.Mailer, settingsSvc, auditWriter)
	lifecycleSvc.SetRoleGate(gate)
	lifecycleSvc.SetOffboardHooks(service.ParseOffboardHooks(cfg.OffboardHooks))
	dashSvc := service.NewDashboardService(deps.Pool, peopleSvc, deptSvc)
	misc := &miscHandler{settings: settingsSvc, dash: dashSvc, directory: directory, depts: deptSvc,
		relevantGroups: func(ctx context.Context) map[string]bool {
			out := map[string]bool{}
			for _, list := range [][]string{cfg.AdminGroups, cfg.ViewerGroups, cfg.LeadGroups} {
				for _, g := range list {
					out[g] = true
				}
			}
			for _, m := range fetcher.FetchAll(ctx) {
				for _, role := range m.Roles {
					out[role.Group] = true
				}
			}
			return out
		}}

	people := &peopleHandler{svc: peopleSvc, lifecycle: lifecycleSvc, chk: chk}
	drafts := &draftHandler{svc: draftSvc, chk: chk}
	prov := &provisionHandler{svc: provSvc, chk: chk}
	auditH := &auditHandler{svc: service.NewAuditService(deps.Pool)}

	require := auth.Require
	global := auth.RequireGlobal

	r.Route("/api", func(api chi.Router) {
		api.Use(den.RequirePaired)
		api.Use(auth.Middleware(cfg, deps.OIDCVerifier, authz.NewResolver(deps.Pool)))

		api.Get("/me", meHandler(cfg))
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
			api.With(require(authz.PeopleRead)).Get("/access-catalog", func(w http.ResponseWriter, req *http.Request) {
				writeJSON(w, http.StatusOK, map[string]any{"modules": fetcher.FetchAll(req.Context())})
			})
			api.With(require(authz.PeopleRead)).Get("/qualification-catalog", roleGate.qualificationCatalog)
			api.With(require(authz.PeopleRead)).Get("/role-requirements", roleGate.listRequirements)
			api.With(global(authz.RequirementsManage)).Post("/role-requirements", roleGate.createRequirement)
			api.With(require(authz.RequirementsDelete)).Delete("/role-requirements/{id}", roleGate.deleteRequirement)
			api.With(require(authz.PeopleRead)).Get("/pending-grants", roleGate.listGrants)
			api.With(require(authz.TrainingOverride)).Post("/pending-grants/{id}/override", roleGate.override)

			api.Route("/people", func(pr chi.Router) {
				pr.With(require(authz.PeopleRead)).Get("/", people.list)
				pr.With(global(authz.PeopleCreate)).Post("/", people.create)
				pr.With(require(authz.PeopleRead)).Get("/{id}", people.get)
				pr.With(require(authz.PeopleWrite)).Put("/{id}", people.update)
				pr.With(require(authz.PeopleRead)).Get("/{id}/access", people.access)
				pr.With(require(authz.AccessManage)).Put("/{id}/access", people.changeAccess)
				pr.With(require(authz.PeoplePause)).Post("/{id}/status", people.setStatus)
				pr.With(global(authz.PeopleOffboard)).Post("/{id}/offboard", people.offboard)
			})

			api.Get("/settings", misc.getSettings)
			api.With(global(authz.SettingsManage)).Put("/settings", misc.updateSettings)
			api.Route("/den", func(dr chi.Router) {
				dr.Use(global(authz.DenManage))
				dr.Get("/", den.StatusHandler)
				dr.Post("/pair", audited(auditWriter, "den.pair", den.PairHandler))
				dr.Post("/sync", audited(auditWriter, "den.sync", den.SyncHandler))
				dr.Delete("/pair", audited(auditWriter, "den.unpair", den.UnpairHandler))
			})
			api.With(require(authz.PeopleWrite)).Get("/check/email", checks.email)
			api.With(require(authz.PeopleWrite)).Get("/check/username", checks.username)
			api.With(require(authz.PeopleWrite)).Get("/address/search", checks.addressSearch)
			api.With(require(authz.PeopleRead)).Get("/dashboard", misc.dashboard)
			api.With(global(authz.IdPGroupsRead)).Get("/idp/groups", misc.idpGroups)
			api.With(global(authz.AuditRead)).Get("/audit", auditH.list)

			api.Route("/departments", func(dr chi.Router) {
				dr.With(require(authz.PeopleRead)).Get("/", depts.list)
				dr.With(global(authz.DepartmentsManage)).Post("/", depts.create)
				dr.With(require(authz.PeopleRead)).Get("/{id}", depts.get)
				dr.With(global(authz.DepartmentsManage)).Put("/{id}", depts.update)
				dr.With(global(authz.DepartmentsManage)).Put("/{id}/leads", depts.setLeads)
				dr.With(global(authz.DepartmentsManage)).Post("/{id}/archive", depts.archive(true))
				dr.With(global(authz.DepartmentsManage)).Post("/{id}/restore", depts.archive(false))
			})

			api.Route("/onboardings", func(or chi.Router) {
				or.With(require(authz.OnboardingManage)).Post("/", onboarding.start)
				or.With(require(authz.PeopleRead)).Get("/{id}", onboarding.get)
				or.With(require(authz.OnboardingManage)).Put("/{id}/details", onboarding.updateDetails)
				or.With(require(authz.AccessManage)).Post("/{id}/complete", onboarding.complete)
			})

			api.Route("/drafts", func(dr chi.Router) {
				dr.With(require(authz.PeopleRead)).Get("/", drafts.list)
				dr.With(require(authz.OnboardingManage)).Post("/", drafts.create)
				dr.With(require(authz.PeopleRead)).Get("/{id}", drafts.get)
				dr.With(require(authz.OnboardingManage)).Put("/{id}/roles", drafts.assignRoles)
				dr.With(require(authz.PeopleRead)).Get("/{id}/provisioning-jobs", prov.listJobs)
			})
		})
	})

	return r
}

func meHandler(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a, _ := actor.FromContext(r.Context())
		out := map[string]any{"username": "", "auth_mode": cfg.AuthMode, "has_access": false, "groups": []string{},
			"roles": []string{}, "permissions": map[string]string{}, "lead_of": []string{}}
		if a != nil {
			out["username"], out["has_access"] = a.Username, a.HasAccess
			if a.Groups != nil {
				out["groups"] = a.Groups
			}
			if a.Roles != nil {
				out["roles"] = a.Roles
			}
			out["permissions"] = authz.Permissions(a)
			leadOf := make([]string, 0, len(a.LeadOf))
			for _, d := range a.LeadOf {
				leadOf = append(leadOf, d.String())
			}
			out["lead_of"] = leadOf
			if a.PersonID != nil {
				out["person_id"] = a.PersonID.String()
			}
		}
		writeJSON(w, http.StatusOK, out)
	}
}
