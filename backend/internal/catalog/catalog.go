// Package catalog fetches role catalogs from every Con Services module.
//
// Contract: each module serves a public `GET /api/role-catalog` returning
//
//	{"module","display_name","description","roles":[{"key","label",
//	  "description","group","assignable","scope","permissions","note"}]}
//
// HR assigns roles by adding the person to the role's IdP `group`.
package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
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
}

// Module is a module's catalog plus fetch status.
type Module struct {
	Module      string `json:"module"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
	Roles       []Role `json:"roles"`
	Source      string `json:"source"`
	Status      string `json:"status"` // ok | error
	Error       string `json:"error,omitempty"`
}

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

// Self is HR's own role catalog.
func Self() Module {
	return Module{
		Module:      "hr",
		DisplayName: "HR",
		Description: "Onboarding, people and access.",
		Source:      "self",
		Status:      "ok",
		Roles: []Role{{
			Key:         "admin",
			Label:       "HR admin",
			Description: "Onboard and offboard people, manage departments and access.",
			Group:       "hr-admin",
			Assignable:  true,
			Scope:       "organization",
			Permissions: []string{"people.manage", "drafts.manage", "provision.run"},
		}},
	}
}

// Fetcher fetches all configured catalogs in parallel.
type Fetcher struct {
	Sources []Source
	Client  *http.Client
}

// FetchAll returns HR's own catalog followed by each source (ok or error).
func (f *Fetcher) FetchAll(ctx context.Context) []Module {
	client := f.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	out := make([]Module, len(f.Sources))
	var wg sync.WaitGroup
	for i, src := range f.Sources {
		wg.Add(1)
		go func(i int, src Source) {
			defer wg.Done()
			out[i] = fetchOne(ctx, client, src)
		}(i, src)
	}
	wg.Wait()
	return append([]Module{Self()}, out...)
}

// DefaultRoles are the assignable roles modules mark as on by default, as
// "<module>:<key>" with their group (modules that fail to load are skipped).
func DefaultRoles(mods []Module) []Role {
	out := []Role{}
	for _, m := range mods {
		if m.Status != "ok" {
			continue
		}
		for _, r := range m.Roles {
			if r.Default && r.Assignable && r.Group != "" {
				r.Key = m.Module + ":" + r.Key
				out = append(out, r)
			}
		}
	}
	return out
}

func fetchOne(ctx context.Context, client *http.Client, src Source) Module {
	fail := func(err error) Module {
		return Module{Module: src.Slug, DisplayName: src.Slug, Source: src.URL, Status: "error", Error: err.Error(), Roles: []Role{}}
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
	if m.Module == "" {
		m.Module = src.Slug
	}
	if m.DisplayName == "" {
		m.DisplayName = m.Module
	}
	if m.Roles == nil {
		m.Roles = []Role{}
	}
	m.Source = src.URL
	m.Status = "ok"
	return m
}
