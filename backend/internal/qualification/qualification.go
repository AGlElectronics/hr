// Package qualification talks to modules that award qualifications (Learn).
//
// Contract:
//
//	GET <catalog source> (public)
//	  {"module","display_name","qualifications":[{"key","name","description","valid_months"}]}
//	GET <holdings url>?subjects=a,b  (Authorization: Bearer <service token>)
//	  {"holdings":{"<subject>":[{"key","expires_at"}]}}
//
// Holdings only list currently valid qualifications. Subjects are IDP user
// ids (HR's external id for a person).
package qualification

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/AGlElectronics/hr/backend/internal/catalog"
)

// Qualification is one entry in a module's qualification catalog.
type Qualification struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	ValidMonths *int   `json:"valid_months,omitempty"`
}

// Module is a module's qualification catalog plus fetch status.
type Module struct {
	Module         string          `json:"module"`
	DisplayName    string          `json:"display_name"`
	Qualifications []Qualification `json:"qualifications"`
	Source         string          `json:"source"`
	Status         string          `json:"status"` // ok | error
	Error          string          `json:"error,omitempty"`
}

// Fetcher fetches all configured qualification catalogs in parallel.
type Fetcher struct {
	Sources []catalog.Source
	Client  *http.Client
}

// FetchAll returns each source's catalog (ok or error).
func (f *Fetcher) FetchAll(ctx context.Context) []Module {
	client := f.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	out := make([]Module, len(f.Sources))
	var wg sync.WaitGroup
	for i, src := range f.Sources {
		wg.Add(1)
		go func(i int, src catalog.Source) {
			defer wg.Done()
			out[i] = fetchOne(ctx, client, src)
		}(i, src)
	}
	wg.Wait()
	return out
}

// Names maps qualification keys to display names from the modules that loaded.
func Names(mods []Module) map[string]string {
	out := map[string]string{}
	for _, m := range mods {
		for _, q := range m.Qualifications {
			if q.Name != "" {
				out[q.Key] = q.Name
			}
		}
	}
	return out
}

func fetchOne(ctx context.Context, client *http.Client, src catalog.Source) Module {
	fail := func(err error) Module {
		return Module{Module: src.Slug, DisplayName: src.Slug, Source: src.URL, Status: "error", Error: err.Error(), Qualifications: []Qualification{}}
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
	defer func() { _ = res.Body.Close() }()
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
	if m.Qualifications == nil {
		m.Qualifications = []Qualification{}
	}
	m.Source = src.URL
	m.Status = "ok"
	return m
}

// Holding is one currently valid qualification of a subject.
type Holding struct {
	Key       string     `json:"key"`
	ExpiresAt *time.Time `json:"expires_at"`
}

// HoldingsClient asks Learn which qualifications people currently hold.
type HoldingsClient struct {
	URL   string
	Token string
	// BatchSize caps subjects per request (default 100).
	BatchSize int
	Client    *http.Client
}

// Holdings returns the valid qualifications per subject. Subjects Learn
// does not know are simply absent from the map. Any failed batch fails the
// whole call, so callers never act on partial data.
func (c *HoldingsClient) Holdings(ctx context.Context, subjects []string) (map[string][]Holding, error) {
	out := map[string][]Holding{}
	if len(subjects) == 0 {
		return out, nil
	}
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	size := c.BatchSize
	if size <= 0 {
		size = 100
	}
	for start := 0; start < len(subjects); start += size {
		end := min(start+size, len(subjects))
		batch, err := c.fetch(ctx, client, subjects[start:end])
		if err != nil {
			return nil, err
		}
		for k, v := range batch {
			out[k] = v
		}
	}
	return out, nil
}

func (c *HoldingsClient) fetch(ctx context.Context, client *http.Client, subjects []string) (map[string][]Holding, error) {
	u, err := url.Parse(c.URL)
	if err != nil {
		return nil, fmt.Errorf("holdings url: %w", err)
	}
	q := u.Query()
	q.Set("subjects", strings.Join(subjects, ","))
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Token)
	res, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("learn holdings: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("learn holdings: status %d", res.StatusCode)
	}
	var parsed struct {
		Holdings map[string][]Holding `json:"holdings"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("learn holdings: decode: %w", err)
	}
	if parsed.Holdings == nil {
		parsed.Holdings = map[string][]Holding{}
	}
	return parsed.Holdings, nil
}

// HeldKeys turns one subject's holdings into a set of keys.
func HeldKeys(hs []Holding) map[string]bool {
	out := make(map[string]bool, len(hs))
	for _, h := range hs {
		out[h.Key] = true
	}
	return out
}
