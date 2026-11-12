package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/AGlElectronics/hr/backend/internal/httperror"
	"github.com/AGlElectronics/hr/backend/internal/service"
)

type checksHandler struct {
	people   *service.PeopleService
	settings *service.SettingsService
	client   *http.Client

	mu       sync.Mutex
	lastCall time.Time
}

// email reports whether an address is valid and its domain accepts mail.
func (h *checksHandler) email(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, service.CheckEmail(r.Context(), r.URL.Query().Get("address")))
}

// username previews the login name for a nickname and whether it is free.
func (h *checksHandler) username(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	self := uuid.Nil
	if v := q.Get("person_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			httperror.Render(w, r, httperror.New(httperror.CodeBadRequest, "person_id must be a uuid"))
			return
		}
		self = id
	}
	out, err := h.people.CheckUsername(r.Context(), self, q.Get("first"), q.Get("last"), q.Get("nickname"))
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// AddressSuggestion is one address the user can pick.
type AddressSuggestion struct {
	Label      string `json:"label"`
	Street     string `json:"street"`
	PostalCode string `json:"postal_code"`
	City       string `json:"city"`
	Country    string `json:"country"`
}

// addressSearch proxies the configured Photon (OpenStreetMap) server so the
// browser never talks to it directly and requests stay rate-limited.
func (h *checksHandler) addressSearch(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if len([]rune(query)) < 3 {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": true, "items": []AddressSuggestion{}})
		return
	}
	st, err := h.settings.Get(r.Context())
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	if st.AddressLookupURL == "" {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false, "items": []AddressSuggestion{}})
		return
	}
	if !h.allow() {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": true, "items": []AddressSuggestion{}, "throttled": true})
		return
	}
	items, err := h.photon(r.Context(), st.AddressLookupURL, query, st.DefaultCountry)
	if err != nil {
		httperror.Render(w, r, httperror.New(httperror.CodeUpstream, "Address suggestions are unavailable right now; type the address by hand.").WithCause(err))
		return
	}
	// provider is shown in the privacy notice next to the address field:
	// what is typed there is sent to this server.
	provider := ""
	if u, err := url.Parse(st.AddressLookupURL); err == nil {
		provider = u.Host
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": true, "items": items, "provider": provider})
}

// allow keeps HR to a few lookups per second in total (public Photon is
// fair-use only).
func (h *checksHandler) allow() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if time.Since(h.lastCall) < 250*time.Millisecond {
		return false
	}
	h.lastCall = time.Now()
	return true
}

func (h *checksHandler) photon(ctx context.Context, base, query, country string) ([]AddressSuggestion, error) {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	v := url.Values{}
	v.Set("q", query)
	v.Set("limit", "8")
	v.Set("lang", "en")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/api/?"+v.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "conservices-hr/1.0")
	res, err := h.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode >= 300 {
		return nil, &upstreamStatus{res.StatusCode}
	}
	var body struct {
		Features []struct {
			Properties struct {
				Name        string `json:"name"`
				Street      string `json:"street"`
				HouseNumber string `json:"housenumber"`
				PostCode    string `json:"postcode"`
				City        string `json:"city"`
				Town        string `json:"town"`
				Village     string `json:"village"`
				CountryCode string `json:"countrycode"`
				Type        string `json:"type"`
			} `json:"properties"`
		} `json:"features"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return nil, err
	}
	out := []AddressSuggestion{}
	seen := map[string]bool{}
	for _, f := range body.Features {
		p := f.Properties
		street := p.Street
		if street == "" && p.Type == "street" {
			street = p.Name
		}
		if street == "" {
			continue
		}
		if p.HouseNumber != "" {
			street += " " + p.HouseNumber
		}
		city := firstNonEmpty(p.City, p.Town, p.Village)
		s := AddressSuggestion{Street: street, PostalCode: p.PostCode, City: city, Country: strings.ToUpper(p.CountryCode)}
		s.Label = strings.Join(nonEmpty(s.Street, strings.TrimSpace(s.PostalCode+" "+s.City), s.Country), ", ")
		if seen[s.Label] {
			continue
		}
		seen[s.Label] = true
		out = append(out, s)
	}
	if country != "" {
		sort.SliceStable(out, func(i, j int) bool { return out[i].Country == country && out[j].Country != country })
	}
	if len(out) > 6 {
		out = out[:6]
	}
	return out, nil
}

type upstreamStatus struct{ status int }

func (e *upstreamStatus) Error() string {
	return "address lookup returned status " + http.StatusText(e.status)
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

func nonEmpty(ss ...string) []string {
	out := []string{}
	for _, s := range ss {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}
