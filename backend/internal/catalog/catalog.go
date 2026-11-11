// Package catalog fetches role catalogs from every Con Services module.
//
// Contract (docs/concepts/role-catalog.md): each module serves
// `GET /api/role-catalog` to HR's service token or a staff sign-in,
// returning
//
//	{"module","display_name","description","revision","roles":[{"key",
//	  "label","description","group","group_display","assignable",
//	  "privileged","dynamic","default","scope","permissions","note"}]}
//
// with the revision also as the ETag. HR assigns roles by adding the person
// to the role's IdP group. Which modules exist comes from
// ROLE_CATALOG_SOURCES (Discover is the hook for Den's paired-module list).
// The last catalog of each module is cached, so roles stay assignable and
// visible while a module is offline.
package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AGlElectronics/hr/backend/internal/authz"
	"github.com/AGlElectronics/hr/backend/internal/groupmatch"
)

// Role is one entry in a module's catalog.
type Role struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Description string `json:"description"`
	// Group is the normalised form, used for comparisons; GroupDisplay the
	// group as the module's admin typed it, used when talking to the IdP.
	Group        string   `json:"group"`
	GroupDisplay string   `json:"group_display,omitempty"`
	Assignable   bool     `json:"assignable"`
	Scope        string   `json:"scope"`
	Permissions  []string `json:"permissions"`
	Note         string   `json:"note,omitempty"`
	// Default roles are ticked for every new onboarding; HR can untick them.
	Default bool `json:"default,omitempty"`
	// Privileged roles (an admin-type role: settings, other people's data
	// or granting access) may only be assigned by HR users with the
	// "assign admin roles" permission.
	Privileged bool `json:"privileged"`
	// Dynamic roles exist because of something created at run time (a
	// Desk mailbox) and disappear with it.
	Dynamic bool `json:"dynamic"`
}

// IdPGroup is the group name HR adds people to: GroupDisplay, else Group.
// The IdP may look groups up case-sensitively, so the normalised form
// could name a different (new) group.
func (r Role) IdPGroup() string {
	if r.GroupDisplay != "" {
		return r.GroupDisplay
	}
	return r.Group
}

// AdminType reports whether the role administers its module: flagged
// privileged, or (catalogs from before the 1.0 contract, which have no
// privileged field) named "admin" / "*-admin" by key or group.
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
	// Revision changes whenever the module's list changes.
	Revision string `json:"revision,omitempty"`
	// ETag is the module's ETag header, sent back as If-None-Match.
	ETag   string `json:"etag,omitempty"`
	Roles  []Role `json:"roles"`
	Source string `json:"source"`
	Status string `json:"status"` // ok | cached | error
	Error  string `json:"error,omitempty"`
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
// HR_ADMIN_GROUPS, HR_VIEWER_GROUPS and HR_LEAD_GROUPS. Every HR role sees
// other people's data, so all three are privileged.
func Self(adminGroup, viewerGroup, leadGroup string) Module {
	role := func(key, label, desc, group, scope string, perms []string) Role {
		g := groupmatch.NewGrant(group)
		return Role{Key: key, Label: label, Description: desc, Group: g.Stored, GroupDisplay: g.Display,
			Assignable: true, Privileged: true, Scope: scope, Permissions: perms}
	}
	lead := role("lead", "Department lead", "Onboard people, change their access, pause them and review new volunteers, "+
		"only in the departments they lead.", leadGroup, "department", authz.PermissionsFor(authz.RoleLead))
	lead.Note = "Put the person on a department's lead list in HR (Departments); HR then gives them this group and the department's lead group."
	m := Module{
		Module:      "hr",
		DisplayName: "HR",
		Description: "Onboarding, people and access.",
		Source:      "self",
		Status:      StatusOK,
		Roles: []Role{
			role("admin", "HR admin", "Everything in HR: onboard and offboard people, departments, access groups, settings, audit log.",
				adminGroup, "organization", authz.PermissionsFor(authz.RoleAdmin)),
			role("viewer", "HR viewer", "Read-only access to people, departments and the audit log.",
				viewerGroup, "organization", authz.PermissionsFor(authz.RoleViewer)),
			lead,
		},
	}
	m.Revision = Revision(m.Roles)
	return m
}

// Revision is a short hash of roles: it changes whenever the list does.
func Revision(roles []Role) string {
	raw, _ := json.Marshal(roles)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:8])
}

// Body is a catalog as served at GET /api/role-catalog (without HR's
// fetch bookkeeping).
func (m Module) Body() map[string]any {
	return map[string]any{"module": m.Module, "display_name": m.DisplayName, "description": m.Description,
		"revision": m.Revision, "roles": m.Roles}
}

// Cache stores the last catalog of each module.
type Cache interface {
	Load(ctx context.Context) (map[string]Module, error)
	Save(ctx context.Context, m Module) error
}

// Fetcher fetches all catalogs in parallel.
type Fetcher struct {
	// Sources is ROLE_CATALOG_SOURCES: replaces a discovered URL for the
	// same slug and adds slugs Den does not know. Alone when Discover is
	// unset or returns nothing (standalone HR).
	Sources []Source
	// Discover lists the modules paired in Den (GET
	// /api/v1/integrations/modules). Nil in tests that pass Sources only.
	Discover func(ctx context.Context) []Source
	Cache    Cache
	Client   *http.Client
	// Token is HR_SERVICE_TOKEN, sent as a Bearer token to every module.
	Token string
	// Self is HR's own catalog, listed first.
	Self Module
	// TTL reuses the last FetchAll result for this long (0: always fetch).
	TTL time.Duration

	mu     sync.Mutex
	last   []Module
	lastAt time.Time
}

// sources is Den's paired-module list, with ROLE_CATALOG_SOURCES applied on
// top: a listed slug replaces the URL Den reported, and a slug Den does not
// know is added. With nothing discovered (standalone, or Den could not be
// asked) the override list is the whole set.
func (f *Fetcher) sources(ctx context.Context) []Source {
	var discovered []Source
	if f.Discover != nil {
		discovered = f.Discover(ctx)
	}
	if len(f.Sources) == 0 {
		return discovered
	}
	if len(discovered) == 0 {
		return f.Sources
	}
	by := map[string]Source{}
	var order []string
	add := func(s Source) {
		if _, ok := by[s.Slug]; !ok {
			order = append(order, s.Slug)
		}
		by[s.Slug] = s
	}
	for _, s := range discovered {
		add(s)
	}
	for _, s := range f.Sources {
		add(s)
	}
	out := make([]Source, 0, len(order))
	for _, slug := range order {
		out = append(out, by[slug])
	}
	return out
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
	cached := map[string]Module{}
	if f.Cache != nil {
		cached, _ = f.Cache.Load(ctx)
	}
	out := make([]Module, len(srcs))
	var wg sync.WaitGroup
	for i, src := range srcs {
		wg.Add(1)
		go func(i int, src Source) {
			defer wg.Done()
			var prev *Module
			if c, ok := cached[src.Slug]; ok && c.Source == src.URL {
				prev = &c
			}
			out[i] = fetchOne(ctx, client, src, f.Token, prev)
		}(i, src)
	}
	wg.Wait()
	if f.Cache != nil {
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

// Groups lists every group of the indexed roles, normalised.
func (ix *Index) Groups() map[string]bool {
	out := map[string]bool{}
	for _, r := range ix.roles {
		if g := groupmatch.Normalize(r.Group); g != "" {
			out[g] = true
		}
	}
	return out
}

// fetchOne reads one module's catalog with HR's service token. prev is the
// cached catalog from the same URL: its ETag is sent as If-None-Match, and a
// 304 reuses it.
func fetchOne(ctx context.Context, client *http.Client, src Source, token string, prev *Module) Module {
	fail := func(err error) Module {
		return Module{Module: src.Slug, DisplayName: src.Slug, Source: src.URL, Status: StatusError, Error: err.Error(), Roles: []Role{}}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src.URL, nil)
	if err != nil {
		return fail(err)
	}
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if prev != nil {
		if tag := prev.ETag; tag != "" {
			req.Header.Set("If-None-Match", tag)
		} else if prev.Revision != "" {
			req.Header.Set("If-None-Match", `"`+prev.Revision+`"`)
		}
	}
	res, err := client.Do(req)
	if err != nil {
		return fail(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	now := time.Now().UTC()
	if res.StatusCode == http.StatusNotModified && prev != nil {
		m := *prev
		m.Source, m.Status, m.Error, m.FetchedAt = src.URL, StatusOK, "", &now
		return m
	}
	if res.StatusCode != http.StatusOK {
		if res.StatusCode == http.StatusUnauthorized {
			return fail(fmt.Errorf("status 401: the module refused HR_SERVICE_TOKEN (missing or different)"))
		}
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
	for i := range m.Roles {
		r := &m.Roles[i]
		if r.GroupDisplay == "" {
			r.GroupDisplay = r.Group
		}
		r.Group = groupmatch.Normalize(r.Group)
	}
	m.ETag = res.Header.Get("ETag")
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
