// Package catalog fetches role catalogs from every Con Services module.
//
// Contract: each module serves a public `GET /api/role-catalog` returning
//
//	{"module","display_name","description","roles":[{"key","label",
//	  "description","group","assignable","privileged","default","scope",
//	  "permissions","note"}]}
//
// HR assigns roles by adding the person to the role's IdP `group`. Which
// modules exist comes from Den's paired-module list (the "modules" section
// of the integration bundle), or from ROLE_CATALOG_SOURCES when set. The
// last catalog of each module is cached, so roles stay assignable and
// visible while a module is offline.
package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AGlElectronics/hr/backend/internal/authz"
)

// Role is one entry in a module's catalog.
type Role struct {
	Key         string   `json:"key"`
	Label       string   `json:"label"`
	Description string   `json:"description"`
	Group       string   `json:"group"`
	Assignable  bool     `json:"assignable"`
	Scope       string   `json:"scope"`
	Permissions []string `json:"permissions"`
	Note        string   `json:"note,omitempty"`
	// Default roles are ticked for every new onboarding; HR can untick them.
	Default bool `json:"default,omitempty"`
	// Privileged roles (administration of a module) may only be assigned
	// by HR users with the "assign admin roles" permission.
	Privileged bool `json:"privileged,omitempty"`
}

// AdminType reports whether the role administers its module: flagged
// privileged, or named "admin" / "*-admin" (by key or group).
func (r Role) AdminType() bool {
	return r.Privileged || r.Key == "admin" || strings.HasSuffix(r.Key, "-admin") || strings.HasSuffix(r.Group, "-admin")
}

// Module statuses.
const (
	StatusOK     = "ok"
	StatusCached = "cached" // module unreachable; last known catalog shown
	StatusError  = "error"  // module unreachable and nothing cached
)

// Module is a module's catalog plus fetch status.
type Module struct {
	Module      string `json:"module"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
	Roles       []Role `json:"roles"`
	Source      string `json:"source"`
	Status      string `json:"status"` // ok | cached | error
	Error       string `json:"error,omitempty"`
	// FetchedAt is when the catalog was last fetched (cached modules).
	FetchedAt *time.Time `json:"fetched_at,omitempty"`
}

// Loaded reports whether the module's roles are known (live or cached).
func (m Module) Loaded() bool { return m.Status == StatusOK || m.Status == StatusCached }

// Source is a module slug + catalog URL.
type Source struct {
	Slug string
	URL  string
}

// ParseSources parses "stash=http://…,events=http://…".
func ParseSources(raw string) []Source {
	out := []Source{}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		slug, url, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		slug, url = strings.TrimSpace(slug), strings.TrimSpace(url)
		if slug != "" && url != "" {
			out = append(out, Source{Slug: slug, URL: url})
		}
	}
	return out
}

// Self is HR's own role catalog. The groups are the first entries of
// HR_ADMIN_GROUPS, HR_VIEWER_GROUPS and HR_LEAD_GROUPS.
func Self(adminGroup, viewerGroup, leadGroup string) Module {
	return Module{
		Module:      "hr",
		DisplayName: "HR",
		Description: "Onboarding, people and access.",
		Source:      "self",
		Status:      StatusOK,
		Roles: []Role{{
			Key:         "admin",
			Label:       "HR admin",
			Description: "Everything in HR: onboard and offboard people, departments, access groups, settings, audit log.",
			Group:       adminGroup,
			Assignable:  true,
			Privileged:  true,
			Scope:       "organization",
			Permissions: authz.PermissionsFor(authz.RoleAdmin),
		}, {
			Key:         "viewer",
			Label:       "HR viewer",
			Description: "Read-only access to people, departments and the audit log.",
			Group:       viewerGroup,
			Assignable:  true,
			Scope:       "organization",
			Permissions: authz.PermissionsFor(authz.RoleViewer),
		}, {
			Key:   "lead",
			Label: "Department lead",
			Description: "Onboard people, change their access, pause them and review new volunteers, " +
				"only in the departments whose lead list includes them.",
			Group:       leadGroup,
			Assignable:  true,
			Scope:       "department",
			Permissions: authz.PermissionsFor(authz.RoleLead),
			Note:        "Put the person on a department's lead list in HR (Departments); HR then gives them this group.",
		}},
	}
}

// Cache stores the last catalog of each module.
type Cache interface {
	Load(ctx context.Context) (map[string]Module, error)
	Save(ctx context.Context, m Module) error
}

// Fetcher fetches all catalogs in parallel.
type Fetcher struct {
	// Sources override discovery (ROLE_CATALOG_SOURCES).
	Sources []Source
	// Discover lists the paired modules (from Den) when Sources is empty.
	Discover func(ctx context.Context) []Source
	Cache    Cache
	Client   *http.Client
	// Self is HR's own catalog, listed first.
	Self Module
	// TTL reuses the last FetchAll result for this long (0: always fetch).
	TTL time.Duration

	mu     sync.Mutex
	last   []Module
	lastAt time.Time
}

func (f *Fetcher) sources(ctx context.Context) []Source {
	if len(f.Sources) > 0 {
		return f.Sources
	}
	if f.Discover != nil {
		return f.Discover(ctx)
	}
	return nil
}

// FetchAll returns HR's own catalog followed by each module (ok, cached or
// error).
func (f *Fetcher) FetchAll(ctx context.Context) []Module {
	if f.TTL > 0 {
		f.mu.Lock()
		if f.last != nil && time.Since(f.lastAt) < f.TTL {
			out := append([]Module(nil), f.last...)
			f.mu.Unlock()
			return out
		}
		f.mu.Unlock()
	}
	client := f.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	srcs := f.sources(ctx)
	out := make([]Module, len(srcs))
	var wg sync.WaitGroup
	for i, src := range srcs {
		wg.Add(1)
		go func(i int, src Source) {
			defer wg.Done()
			out[i] = fetchOne(ctx, client, src)
		}(i, src)
	}
	wg.Wait()
	if f.Cache != nil {
		cached, _ := f.Cache.Load(ctx)
		for i := range out {
			if out[i].Status == StatusOK {
				_ = f.Cache.Save(ctx, out[i])
				continue
			}
			if c, ok := cached[out[i].Module]; ok {
				c.Status, c.Error, c.Source = StatusCached, out[i].Error, out[i].Source
				out[i] = c
			}
		}
	}
	all := append([]Module{f.Self}, out...)
	if f.TTL > 0 {
		f.mu.Lock()
		f.last, f.lastAt = all, time.Now()
		f.mu.Unlock()
	}
	return append([]Module(nil), all...)
}

// Invalidate forgets the memoized FetchAll result.
func (f *Fetcher) Invalidate() {
	f.mu.Lock()
	f.last = nil
	f.mu.Unlock()
}

// DefaultRoles are the assignable roles modules mark as on by default, as
// "<module>:<key>" with their group (modules that fail to load are skipped).
func DefaultRoles(mods []Module) []Role {
	out := []Role{}
	for _, m := range mods {
		if !m.Loaded() {
			continue
		}
		for _, r := range m.Roles {
			if r.Default && r.Assignable && r.Group != "" && !r.AdminType() {
				r.Key = m.Module + ":" + r.Key
				out = append(out, r)
			}
		}
	}
	return out
}

// RoleState is what HR knows about a role key.
type RoleState int

const (
	// RoleUnknownModule: the module's catalog is not loaded (not paired, or
	// never reachable).
	RoleUnknownModule RoleState = iota
	// RoleKnown: the module publishes the role.
	RoleKnown
	// RoleRetired: the module's catalog is loaded but no longer lists it.
	RoleRetired
)

// Index looks roles up by "<module>:<key>".
type Index struct {
	roles   map[string]Role
	modules map[string]bool
}

// NewIndex indexes loaded modules.
func NewIndex(mods []Module) *Index {
	ix := &Index{roles: map[string]Role{}, modules: map[string]bool{}}
	for _, m := range mods {
		if !m.Loaded() {
			continue
		}
		ix.modules[m.Module] = true
		for _, r := range m.Roles {
			ix.roles[m.Module+":"+r.Key] = r
		}
	}
	return ix
}

// Lookup returns the role and its state.
func (ix *Index) Lookup(key string) (Role, RoleState) {
	if r, ok := ix.roles[key]; ok {
		return r, RoleKnown
	}
	mod, _, _ := strings.Cut(key, ":")
	if ix.modules[mod] {
		return Role{}, RoleRetired
	}
	return Role{}, RoleUnknownModule
}

// Groups lists every group of the indexed roles.
func (ix *Index) Groups() map[string]bool {
	out := map[string]bool{}
	for _, r := range ix.roles {
		if r.Group != "" {
			out[r.Group] = true
		}
	}
	return out
}

func fetchOne(ctx context.Context, client *http.Client, src Source) Module {
	fail := func(err error) Module {
		return Module{Module: src.Slug, DisplayName: src.Slug, Source: src.URL, Status: StatusError, Error: err.Error(), Roles: []Role{}}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src.URL, nil)
	if err != nil {
		return fail(err)
	}
	req.Header.Set("Accept", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return fail(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode != http.StatusOK {
		return fail(fmt.Errorf("status %d", res.StatusCode))
	}
	var m Module
	if err := json.Unmarshal(body, &m); err != nil {
		return fail(fmt.Errorf("decode: %w", err))
	}
	// A module only speaks for itself.
	m.Module = src.Slug
	if m.DisplayName == "" {
		m.DisplayName = m.Module
	}
	if m.Roles == nil {
		m.Roles = []Role{}
	}
	now := time.Now().UTC()
	m.Source, m.Status, m.Error, m.FetchedAt = src.URL, StatusOK, "", &now
	return m
}

// PGCache stores catalogs in the catalog_cache table.
type PGCache struct{ Pool *pgxpool.Pool }

// Load returns every cached catalog.
func (c *PGCache) Load(ctx context.Context) (map[string]Module, error) {
	out := map[string]Module{}
	if c == nil || c.Pool == nil {
		return out, nil
	}
	rows, err := c.Pool.Query(ctx, `SELECT module, payload, fetched_at FROM catalog_cache`)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var slug string
		var raw []byte
		var at time.Time
		if err := rows.Scan(&slug, &raw, &at); err != nil {
			return out, err
		}
		var m Module
		if json.Unmarshal(raw, &m) == nil {
			m.Module, m.FetchedAt = slug, &at
			out[slug] = m
		}
	}
	return out, rows.Err()
}

// Save stores a freshly fetched catalog.
func (c *PGCache) Save(ctx context.Context, m Module) error {
	if c == nil || c.Pool == nil {
		return nil
	}
	m.Status, m.Error = "", ""
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	_, err = c.Pool.Exec(ctx, `
INSERT INTO catalog_cache (module, payload, source, fetched_at) VALUES ($1, $2, $3, now())
ON CONFLICT (module) DO UPDATE SET payload = EXCLUDED.payload, source = EXCLUDED.source, fetched_at = now()`, m.Module, raw, m.Source)
	return err
}

// DenModule is one entry of the "modules" section Den sends in the
// integration bundle: a module paired with Den.
type DenModule struct {
	Slug           string `json:"slug"`
	RoleCatalogURL string `json:"role_catalog_url"`
	// InternalURL is the module backend's in-cluster address; the catalog
	// is then <internal_url>/api/role-catalog.
	InternalURL string `json:"internal_url"`
}

// DenSources reads the paired modules from the bundle denlink stores in
// den_link (nothing while HR is standalone).
func DenSources(pool *pgxpool.Pool) func(ctx context.Context) []Source {
	return func(ctx context.Context) []Source {
		if pool == nil {
			return nil
		}
		var raw []byte
		err := pool.QueryRow(ctx, `SELECT bundle->'modules' FROM den_link WHERE id = 1 AND bundle ? 'modules'`).Scan(&raw)
		if errors.Is(err, pgx.ErrNoRows) || err != nil {
			return nil
		}
		return ParseDenModules(raw)
	}
}

// ParseDenModules turns Den's module list into catalog sources (HR itself
// is skipped).
func ParseDenModules(raw []byte) []Source {
	var mods []DenModule
	if json.Unmarshal(raw, &mods) != nil {
		return nil
	}
	out := []Source{}
	for _, m := range mods {
		slug := strings.TrimSpace(m.Slug)
		if slug == "" || slug == "hr" {
			continue
		}
		u := strings.TrimSpace(m.RoleCatalogURL)
		if u == "" && strings.TrimSpace(m.InternalURL) != "" {
			u = strings.TrimRight(strings.TrimSpace(m.InternalURL), "/") + "/api/role-catalog"
		}
		if u != "" {
			out = append(out, Source{Slug: slug, URL: u})
		}
	}
	return out
}
