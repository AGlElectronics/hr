package provisioners

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

// KeycloakConfig is the Admin API client settings for IdP provisioning.
type KeycloakConfig struct {
	// BaseURL is the Keycloak origin reachable from HR (e.g. http://keycloak:8080).
	BaseURL string
	Realm   string
	// ClientID / ClientSecret are a confidential client with service account
	// roles to manage users (hr-provisioner).
	ClientID     string
	ClientSecret string
	// SetupClientID / SetupRedirectURI are where the set-password email link
	// lands the user afterwards (normally the portal, e.g. den-spa).
	SetupClientID    string
	SetupRedirectURI string
	// SetupLinkLifespan is how long the set-password link stays valid.
	SetupLinkLifespan time.Duration
	HTTPClient        *http.Client
}

// KeycloakIdP creates/updates users in Keycloak via the Admin REST API.
type KeycloakIdP struct {
	cfg    KeycloakConfig
	logger zerolog.Logger

	mu          sync.Mutex
	token       string
	tokenExpiry time.Time
	groupCache  map[string]string // name -> id
}

// NewKeycloakIdP builds a Keycloak-backed IdPProvisioner.
func NewKeycloakIdP(cfg KeycloakConfig, logger zerolog.Logger) (*KeycloakIdP, error) {
	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	cfg.Realm = strings.TrimSpace(cfg.Realm)
	if cfg.BaseURL == "" || cfg.Realm == "" || cfg.ClientID == "" || cfg.ClientSecret == "" {
		return nil, fmt.Errorf("keycloak idp: BaseURL, Realm, ClientID and ClientSecret are required")
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 20 * time.Second}
	}
	if cfg.SetupLinkLifespan <= 0 {
		cfg.SetupLinkLifespan = 72 * time.Hour
	}
	return &KeycloakIdP{
		cfg:        cfg,
		logger:     logger.With().Str("component", "keycloak_idp").Logger(),
		groupCache: map[string]string{},
	}, nil
}

// errUnauthorized marks an Admin API 401 (stale/rotated service token).
var errUnauthorized = errors.New("keycloak admin: unauthorized")

// withFreshTokenRetry runs op, and once more with a new service-account token
// if Keycloak rejected the cached one (e.g. after a Keycloak restart).
func (k *KeycloakIdP) withFreshTokenRetry(op func() error) error {
	err := op()
	if errors.Is(err, errUnauthorized) {
		k.mu.Lock()
		k.token = ""
		k.groupCache = map[string]string{}
		k.mu.Unlock()
		err = op()
	}
	return err
}

// Provision creates or updates the user, syncs groups, and either emails a
// set-password link or sets a temporary password for brand-new users.
func (k *KeycloakIdP) Provision(ctx context.Context, u IdPUser) (IdPResult, error) {
	var res IdPResult
	err := k.withFreshTokenRetry(func() error {
		var err error
		res, err = k.provisionOnce(ctx, u)
		return err
	})
	return res, err
}

func (k *KeycloakIdP) provisionOnce(ctx context.Context, u IdPUser) (IdPResult, error) {
	if ctx.Err() != nil {
		return IdPResult{}, ctx.Err()
	}
	username := strings.TrimSpace(u.Username)
	if username == "" {
		username = strings.TrimSpace(u.Email)
	}
	if username == "" {
		return IdPResult{}, fmt.Errorf("keycloak idp: username or email required")
	}

	token, err := k.accessToken(ctx)
	if err != nil {
		return IdPResult{}, err
	}

	first, last := u.FirstName, u.LastName
	if first == "" && last == "" {
		first, last = splitDisplayName(u.DisplayName)
	}
	attrs := map[string]string{}
	for key, v := range u.Attributes {
		attrs[key] = v
	}
	attrs[AttrHRPersonID] = u.HRPersonID.String()

	userID, err := k.linkedOrOwnedUser(ctx, token, u, username)
	if err != nil {
		return IdPResult{}, err
	}
	created := false
	if userID == "" {
		userID, err = k.createUser(ctx, token, map[string]any{
			"username":      username,
			"enabled":       true,
			"emailVerified": true,
			"email":         u.Email,
			"firstName":     first,
			"lastName":      last,
			"attributes":    attrValues(attrs),
		})
		if err != nil {
			return IdPResult{}, err
		}
		created = true
	} else {
		changes := map[string]any{"enabled": true, "email": u.Email, "firstName": first, "lastName": last}
		if err := k.patchUserOnce(ctx, token, userID, changes, attrs); err != nil {
			return IdPResult{}, err
		}
	}

	applied, err := k.syncGroups(ctx, token, userID, u.Groups)
	if err != nil {
		return IdPResult{}, err
	}

	res := IdPResult{ExternalID: userID, GroupsApplied: applied, Created: created}
	if created {
		if u.SendPasswordSetup {
			if err := k.sendPasswordSetup(ctx, token, userID); err != nil {
				res.PasswordSetupError = err.Error()
			} else {
				res.PasswordSetupEmailSent = true
			}
		}
		if !res.PasswordSetupEmailSent {
			res.TemporaryPassword, err = randomPassword(20)
			if err != nil {
				return IdPResult{}, err
			}
			if err := k.setTemporaryPassword(ctx, token, userID, res.TemporaryPassword); err != nil {
				return IdPResult{}, err
			}
		}
	}

	k.logger.Info().
		Str("username", username).
		Str("user_id", userID).
		Bool("created", created).
		Bool("setup_email_sent", res.PasswordSetupEmailSent).
		Strs("groups", applied).
		Msg("keycloak idp provision")
	return res, nil
}

// Keycloak user attributes HR writes. They must be declared in the realm's
// user profile (see infra/keycloak/apply-mvp.sh), or Keycloak drops them.
const (
	AttrHRPersonID   = "hrPersonId"
	AttrNickname     = "nickname"
	AttrPhone        = "phone"
	AttrOrgEmail     = "orgEmail"
	AttrPrivateEmail = "privateEmail"
)

// linkedOrOwnedUser returns the account to update: the linked ExternalID if
// it still exists, else an account with the username that belongs to this
// person. "" means create a new one. A username owned by someone else is a
// *UsernameTakenError.
func (k *KeycloakIdP) linkedOrOwnedUser(ctx context.Context, token string, u IdPUser, username string) (string, error) {
	if u.ExternalID != "" {
		err := k.adminJSON(ctx, token, http.MethodGet, "/users/"+url.PathEscape(u.ExternalID), nil, nil)
		if err == nil {
			return u.ExternalID, nil
		}
		if !errors.Is(asAccountError(err), ErrAccountNotFound) {
			return "", err
		}
	}
	existingID, err := k.findUserID(ctx, token, username)
	if err != nil || existingID == "" {
		return "", err
	}
	var rep struct {
		Email      string              `json:"email"`
		Attributes map[string][]string `json:"attributes"`
	}
	if err := k.adminJSON(ctx, token, http.MethodGet, "/users/"+url.PathEscape(existingID), nil, &rep); err != nil {
		return "", err
	}
	owner := ""
	if v := rep.Attributes[AttrHRPersonID]; len(v) > 0 {
		owner = v[0]
	}
	switch {
	case owner == u.HRPersonID.String():
		return existingID, nil
	case owner == "" && rep.Email != "" && strings.EqualFold(rep.Email, u.Email):
		return existingID, nil
	default:
		return "", &UsernameTakenError{Username: username}
	}
}

func attrValues(attrs map[string]string) map[string][]string {
	out := map[string][]string{}
	for key, v := range attrs {
		if v = strings.TrimSpace(v); v != "" {
			out[key] = []string{v}
		}
	}
	return out
}

// DeleteAccount removes the user from the IdP.
func (k *KeycloakIdP) DeleteAccount(ctx context.Context, userID string) error {
	return k.withFreshTokenRetry(func() error {
		token, err := k.accessToken(ctx)
		if err != nil {
			return err
		}
		return asAccountError(k.adminJSON(ctx, token, http.MethodDelete, "/users/"+url.PathEscape(userID), nil, nil))
	})
}

// sendPasswordSetup has Keycloak email the user a link to set their password
// (UPDATE_PASSWORD required action). Needs SMTP configured on the realm.
func (k *KeycloakIdP) sendPasswordSetup(ctx context.Context, token, userID string) error {
	q := url.Values{}
	q.Set("lifespan", fmt.Sprintf("%d", int(k.cfg.SetupLinkLifespan.Seconds())))
	if k.cfg.SetupClientID != "" && k.cfg.SetupRedirectURI != "" {
		q.Set("client_id", k.cfg.SetupClientID)
		q.Set("redirect_uri", k.cfg.SetupRedirectURI)
	}
	path := "/users/" + url.PathEscape(userID) + "/execute-actions-email?" + q.Encode()
	return k.adminJSON(ctx, token, http.MethodPut, path, []string{"UPDATE_PASSWORD"}, nil)
}

// EnsureGroups creates any missing top-level groups.
func (k *KeycloakIdP) EnsureGroups(ctx context.Context, names []string) error {
	return k.withFreshTokenRetry(func() error { return k.ensureGroupsOnce(ctx, names) })
}

// ListGroups returns every realm group (flattened) with its direct members.
func (k *KeycloakIdP) ListGroups(ctx context.Context) ([]GroupInfo, error) {
	var out []GroupInfo
	err := k.withFreshTokenRetry(func() error {
		token, err := k.accessToken(ctx)
		if err != nil {
			return err
		}
		type kcGroup struct {
			ID        string    `json:"id"`
			Name      string    `json:"name"`
			Path      string    `json:"path"`
			SubGroups []kcGroup `json:"subGroups"`
		}
		var top []kcGroup
		if err := k.adminJSON(ctx, token, http.MethodGet, "/groups?briefRepresentation=false&max=1000", nil, &top); err != nil {
			return err
		}
		out = out[:0]
		var walk func(gs []kcGroup) error
		walk = func(gs []kcGroup) error {
			for _, g := range gs {
				var members []struct {
					ID        string `json:"id"`
					Username  string `json:"username"`
					Email     string `json:"email"`
					FirstName string `json:"firstName"`
					LastName  string `json:"lastName"`
					Enabled   bool   `json:"enabled"`
				}
				if err := k.adminJSON(ctx, token, http.MethodGet, "/groups/"+url.PathEscape(g.ID)+"/members?max=1000", nil, &members); err != nil {
					return err
				}
				info := GroupInfo{ID: g.ID, Name: g.Name, Path: g.Path, Members: make([]GroupMember, 0, len(members))}
				for _, m := range members {
					info.Members = append(info.Members, GroupMember{ID: m.ID, Username: m.Username, Email: m.Email,
						FirstName: m.FirstName, LastName: m.LastName, Enabled: m.Enabled})
				}
				out = append(out, info)
				if err := walk(g.SubGroups); err != nil {
					return err
				}
			}
			return nil
		}
		return walk(top)
	})
	return out, err
}

// UpdateMembership adds the user to `add` groups (creating missing ones) and
// removes them from `remove` groups that exist.
func (k *KeycloakIdP) UpdateMembership(ctx context.Context, userID string, add, remove []string) error {
	return k.withFreshTokenRetry(func() error {
		token, err := k.accessToken(ctx)
		if err != nil {
			return err
		}
		if err := k.adminJSON(ctx, token, http.MethodGet, "/users/"+url.PathEscape(userID), nil, nil); err != nil {
			return asAccountError(err)
		}
		if _, err := k.syncGroups(ctx, token, userID, add); err != nil {
			return err
		}
		for _, name := range uniqueNonEmpty(remove) {
			gid, err := k.groupID(ctx, token, name)
			if err != nil {
				return err
			}
			if gid == "" {
				continue
			}
			path := "/users/" + url.PathEscape(userID) + "/groups/" + url.PathEscape(gid)
			if err := k.adminJSON(ctx, token, http.MethodDelete, path, nil, nil); err != nil {
				return err
			}
		}
		return nil
	})
}

// AdminError is a non-2xx Keycloak Admin API response.
type AdminError struct {
	Method, Path string
	Status       int
	Body         string
}

func (e *AdminError) Error() string {
	return fmt.Sprintf("keycloak admin %s %s: status %d: %s", e.Method, e.Path, e.Status, e.Body)
}

// GetAccount returns the user's login state, or ErrAccountNotFound.
func (k *KeycloakIdP) GetAccount(ctx context.Context, userID string) (*AccountInfo, error) {
	var out *AccountInfo
	err := k.withFreshTokenRetry(func() error {
		token, err := k.accessToken(ctx)
		if err != nil {
			return err
		}
		var rep struct {
			Username string `json:"username"`
			Enabled  bool   `json:"enabled"`
		}
		if err := k.adminJSON(ctx, token, http.MethodGet, "/users/"+url.PathEscape(userID), nil, &rep); err != nil {
			return asAccountError(err)
		}
		out = &AccountInfo{Username: rep.Username, Enabled: rep.Enabled}
		return nil
	})
	return out, err
}

// asAccountError maps a 404 on a user to ErrAccountNotFound.
func asAccountError(err error) error {
	var ae *AdminError
	if errors.As(err, &ae) && ae.Status == http.StatusNotFound {
		return ErrAccountNotFound
	}
	return err
}

// SetEnabled enables or disables login for the user.
func (k *KeycloakIdP) SetEnabled(ctx context.Context, userID string, enabled bool) error {
	return k.patchUser(ctx, userID, map[string]any{"enabled": enabled}, nil)
}

// UpdateProfile syncs email, names and attributes, and renames the login
// when p.Username is set (needs editUsernameAllowed on the realm).
func (k *KeycloakIdP) UpdateProfile(ctx context.Context, userID string, p Profile) error {
	changes := map[string]any{"email": p.Email, "firstName": p.FirstName, "lastName": p.LastName}
	if u := strings.TrimSpace(p.Username); u != "" {
		changes["username"] = u
	}
	return k.patchUser(ctx, userID, changes, p.Attributes)
}

// UsernameOwner looks the username up in the realm.
func (k *KeycloakIdP) UsernameOwner(ctx context.Context, username string) (string, bool, error) {
	owner, found := "", false
	err := k.withFreshTokenRetry(func() error {
		token, err := k.accessToken(ctx)
		if err != nil {
			return err
		}
		id, err := k.findUserID(ctx, token, username)
		if err != nil || id == "" {
			return err
		}
		var rep struct {
			Attributes map[string][]string `json:"attributes"`
		}
		if err := k.adminJSON(ctx, token, http.MethodGet, "/users/"+url.PathEscape(id), nil, &rep); err != nil {
			return err
		}
		found = true
		if v := rep.Attributes[AttrHRPersonID]; len(v) > 0 {
			owner = v[0]
		}
		return nil
	})
	return owner, found, err
}

func (k *KeycloakIdP) patchUser(ctx context.Context, userID string, changes map[string]any, attrs map[string]string) error {
	return k.withFreshTokenRetry(func() error {
		token, err := k.accessToken(ctx)
		if err != nil {
			return err
		}
		return k.patchUserOnce(ctx, token, userID, changes, attrs)
	})
}

// patchUserOnce round-trips the full representation: user-profile
// validation rejects partial updates that omit required attributes. attrs
// are merged into the existing attributes; empty values remove them.
func (k *KeycloakIdP) patchUserOnce(ctx context.Context, token, userID string, changes map[string]any, attrs map[string]string) error {
	var rep map[string]any
	if err := k.adminJSON(ctx, token, http.MethodGet, "/users/"+url.PathEscape(userID), nil, &rep); err != nil {
		return asAccountError(err)
	}
	for key, v := range changes {
		rep[key] = v
	}
	if len(attrs) > 0 {
		merged, _ := rep["attributes"].(map[string]any)
		if merged == nil {
			merged = map[string]any{}
		}
		for key, v := range attrs {
			if v = strings.TrimSpace(v); v == "" {
				delete(merged, key)
			} else {
				merged[key] = []string{v}
			}
		}
		rep["attributes"] = merged
	}
	return k.adminJSON(ctx, token, http.MethodPut, "/users/"+url.PathEscape(userID), rep, nil)
}

func (k *KeycloakIdP) ensureGroupsOnce(ctx context.Context, names []string) error {
	token, err := k.accessToken(ctx)
	if err != nil {
		return err
	}
	for _, name := range uniqueNonEmpty(names) {
		gid, err := k.groupID(ctx, token, name)
		if err != nil {
			return err
		}
		if gid == "" {
			if _, err := k.createGroup(ctx, token, name); err != nil {
				return err
			}
		}
	}
	return nil
}

func (k *KeycloakIdP) accessToken(ctx context.Context) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.token != "" && time.Now().Before(k.tokenExpiry.Add(-30*time.Second)) {
		return k.token, nil
	}
	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", k.cfg.ClientID)
	form.Set("client_secret", k.cfg.ClientSecret)
	endpoint := fmt.Sprintf("%s/realms/%s/protocol/openid-connect/token", k.cfg.BaseURL, url.PathEscape(k.cfg.Realm))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := k.cfg.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("keycloak token: %w", err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		return "", fmt.Errorf("keycloak token: status %d: %s", res.StatusCode, truncate(string(body), 300))
	}
	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tok); err != nil {
		return "", fmt.Errorf("keycloak token decode: %w", err)
	}
	if tok.AccessToken == "" {
		return "", fmt.Errorf("keycloak token: empty access_token")
	}
	k.token = tok.AccessToken
	exp := tok.ExpiresIn
	if exp <= 0 {
		exp = 60
	}
	k.tokenExpiry = time.Now().Add(time.Duration(exp) * time.Second)
	return k.token, nil
}

func (k *KeycloakIdP) findUserID(ctx context.Context, token, username string) (string, error) {
	q := url.Values{}
	q.Set("username", username)
	q.Set("exact", "true")
	var users []struct {
		ID string `json:"id"`
	}
	if err := k.adminJSON(ctx, token, http.MethodGet, "/users?"+q.Encode(), nil, &users); err != nil {
		return "", err
	}
	if len(users) == 0 {
		return "", nil
	}
	return users[0].ID, nil
}

func (k *KeycloakIdP) createUser(ctx context.Context, token string, payload map[string]any) (string, error) {
	res, err := k.adminRaw(ctx, token, http.MethodPost, "/users", payload)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusCreated && res.StatusCode != http.StatusNoContent {
		return "", fmt.Errorf("keycloak create user: status %d: %s", res.StatusCode, truncate(string(body), 300))
	}
	loc := res.Header.Get("Location")
	if loc == "" {
		// Fallback lookup by username.
		username, _ := payload["username"].(string)
		return k.findUserID(ctx, token, username)
	}
	parts := strings.Split(strings.TrimRight(loc, "/"), "/")
	return parts[len(parts)-1], nil
}

func (k *KeycloakIdP) setTemporaryPassword(ctx context.Context, token, userID, password string) error {
	body := map[string]any{
		"type":      "password",
		"value":     password,
		"temporary": true,
	}
	return k.adminJSON(ctx, token, http.MethodPut, "/users/"+url.PathEscape(userID)+"/reset-password", body, nil)
}

func (k *KeycloakIdP) syncGroups(ctx context.Context, token, userID string, groups []string) ([]string, error) {
	applied := make([]string, 0, len(groups))
	for _, name := range uniqueNonEmpty(groups) {
		gid, err := k.groupID(ctx, token, name)
		if err != nil {
			return applied, err
		}
		if gid == "" {
			gid, err = k.createGroup(ctx, token, name)
			if err != nil {
				return applied, err
			}
		}
		path := "/users/" + url.PathEscape(userID) + "/groups/" + url.PathEscape(gid)
		if err := k.adminJSON(ctx, token, http.MethodPut, path, map[string]any{}, nil); err != nil {
			return applied, err
		}
		applied = append(applied, name)
	}
	return applied, nil
}

func (k *KeycloakIdP) groupID(ctx context.Context, token, name string) (string, error) {
	k.mu.Lock()
	if id, ok := k.groupCache[name]; ok {
		k.mu.Unlock()
		return id, nil
	}
	k.mu.Unlock()

	q := url.Values{}
	q.Set("search", name)
	q.Set("exact", "true")
	var groups []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := k.adminJSON(ctx, token, http.MethodGet, "/groups?"+q.Encode(), nil, &groups); err != nil {
		return "", err
	}
	for _, g := range groups {
		if g.Name == name {
			k.mu.Lock()
			k.groupCache[name] = g.ID
			k.mu.Unlock()
			return g.ID, nil
		}
	}
	return "", nil
}

// createGroup adds a top-level realm group. Module role catalogs name their
// groups; the first provision for a new role creates it in Keycloak.
func (k *KeycloakIdP) createGroup(ctx context.Context, token, name string) (string, error) {
	res, err := k.adminRaw(ctx, token, http.MethodPost, "/groups", map[string]any{"name": name})
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusCreated && res.StatusCode != http.StatusConflict {
		return "", fmt.Errorf("keycloak create group %q: status %d: %s", name, res.StatusCode, truncate(string(body), 300))
	}
	if loc := res.Header.Get("Location"); loc != "" {
		parts := strings.Split(strings.TrimRight(loc, "/"), "/")
		id := parts[len(parts)-1]
		k.mu.Lock()
		k.groupCache[name] = id
		k.mu.Unlock()
		k.logger.Info().Str("group", name).Msg("keycloak group created")
		return id, nil
	}
	id, err := k.groupID(ctx, token, name)
	if err != nil {
		return "", err
	}
	if id == "" {
		return "", fmt.Errorf("keycloak group %q could not be created", name)
	}
	return id, nil
}

func (k *KeycloakIdP) adminJSON(ctx context.Context, token, method, path string, payload any, dest any) error {
	res, err := k.adminRaw(ctx, token, method, path, payload)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("%w: %s %s", errUnauthorized, method, path)
	}
	if res.StatusCode >= 300 {
		return &AdminError{Method: method, Path: path, Status: res.StatusCode, Body: truncate(string(body), 300)}
	}
	if dest == nil || res.StatusCode == http.StatusNoContent || len(body) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, dest); err != nil {
		return fmt.Errorf("keycloak admin decode %s: %w", path, err)
	}
	return nil
}

func (k *KeycloakIdP) adminRaw(ctx context.Context, token, method, path string, payload any) (*http.Response, error) {
	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(b)
	}
	endpoint := fmt.Sprintf("%s/admin/realms/%s%s", k.cfg.BaseURL, url.PathEscape(k.cfg.Realm), path)
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return k.cfg.HTTPClient.Do(req)
}

func splitDisplayName(name string) (first, last string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "User", ""
	}
	parts := strings.Fields(name)
	if len(parts) == 1 {
		return parts[0], ""
	}
	return parts[0], strings.Join(parts[1:], " ")
}

func randomPassword(n int) (string, error) {
	const alphabet = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789!@#$"
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	out := make([]byte, n)
	for i := range b {
		out[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(out), nil
}

func uniqueNonEmpty(in []string) []string {
	return mergeUniqueStrings(nil, in)
}

func mergeUniqueStrings(a, b []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(a)+len(b))
	for _, v := range append(append([]string{}, a...), b...) {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
