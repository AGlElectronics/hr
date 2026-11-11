package provisioners

import (
	"bytes"
	"context"
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
	// LinkOnly is IDP_PROVISIONER_MODE=link: HR finds existing accounts
	// (by id, username or email) and manages membership of its groups, but
	// never creates, renames or deletes accounts and never creates groups.
	LinkOnly bool
	// GroupParent is the path of the group HR's groups live under
	// (KEYCLOAK_GROUP_PARENT, e.g. "/conservices"); empty: top level.
	GroupParent string
	// GroupPrefix is put in front of every group name HR assigns
	// (KEYCLOAK_GROUP_PREFIX, e.g. "cs-").
	GroupPrefix string
	// Attributes HR may write (KEYCLOAK_USER_ATTRIBUTES). HR writes an
	// attribute only if it is also declared in the realm's user profile.
	Attributes []string
	HTTPClient *http.Client
}

// DefaultAttributes are the user attributes HR writes by default.
var DefaultAttributes = []string{AttrHRPersonID, AttrNickname, AttrOrgEmail}

// KeycloakIdP creates/updates users in Keycloak via the Admin REST API.
type KeycloakIdP struct {
	cfg    KeycloakConfig
	logger zerolog.Logger

	mu          sync.Mutex
	token       string
	tokenExpiry time.Time
	groupCache  map[string]string // path -> id
	declared    map[string]bool   // user-profile attributes; nil = unknown
	declaredAt  time.Time
}

// ErrNoAccount means link mode found no existing account for the person.
var ErrNoAccount = errors.New("no account found in the identity provider; ask its administrators to create one, then try again")

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
	if len(cfg.Attributes) == 0 {
		cfg.Attributes = DefaultAttributes
	}
	cfg.GroupParent = "/" + strings.Trim(strings.TrimSpace(cfg.GroupParent), "/")
	if cfg.GroupParent == "/" {
		cfg.GroupParent = ""
	}
	return &KeycloakIdP{
		cfg:        cfg,
		logger:     logger.With().Str("component", "keycloak_idp").Logger(),
		groupCache: map[string]string{},
	}, nil
}

// LinkOnly reports link mode.
func (k *KeycloakIdP) LinkOnly() bool { return k.cfg.LinkOnly }

// GroupPath is the full Keycloak path of an HR group name.
func (k *KeycloakIdP) GroupPath(name string) string {
	return k.cfg.GroupParent + "/" + k.cfg.GroupPrefix + strings.TrimPrefix(name, "/")
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

// Provision creates (manage mode) or finds (link mode) the user, updates it,
// syncs groups, and emails a brand-new user a set-password link when asked.
// It never sets a password itself.
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
	attrs = k.allowedAttributes(ctx, token, attrs)

	userID, err := k.linkedOrOwnedUser(ctx, token, u, username)
	if err != nil {
		return IdPResult{}, err
	}
	created := false
	switch {
	case userID == "" && k.cfg.LinkOnly:
		return IdPResult{}, ErrNoAccount
	case userID == "":
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
	case k.cfg.LinkOnly:
		// The convention owns the account: only HR's own attribute.
		if err := k.patchUserOnce(ctx, token, userID, map[string]any{}, map[string]string{AttrHRPersonID: attrs[AttrHRPersonID]}); err != nil {
			return IdPResult{}, err
		}
	default:
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
	if created && u.SendPasswordSetup {
		if err := k.sendPasswordSetup(ctx, token, userID); err != nil {
			res.PasswordSetupError = err.Error()
		} else {
			res.PasswordSetupEmailSent = true
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

// Keycloak user attributes HR can write. HR writes only those listed in
// KEYCLOAK_USER_ATTRIBUTES that the realm's user profile declares.
const (
	AttrHRPersonID   = "hrPersonId"
	AttrNickname     = "nickname"
	AttrPhone        = "phone"
	AttrOrgEmail     = "orgEmail"
	AttrPrivateEmail = "privateEmail"
)

// allowedAttributes keeps the configured attributes the realm declares.
func (k *KeycloakIdP) allowedAttributes(ctx context.Context, token string, attrs map[string]string) map[string]string {
	declared := k.declaredAttributes(ctx, token)
	out := map[string]string{}
	for _, name := range k.cfg.Attributes {
		v, ok := attrs[name]
		if !ok {
			continue
		}
		if declared != nil && !declared[name] {
			continue
		}
		out[name] = v
	}
	return out
}

// declaredAttributes reads the realm's user profile (cached for ten
// minutes); nil when it cannot be read.
func (k *KeycloakIdP) declaredAttributes(ctx context.Context, token string) map[string]bool {
	k.mu.Lock()
	if time.Since(k.declaredAt) < 10*time.Minute {
		d := k.declared
		k.mu.Unlock()
		return d
	}
	k.mu.Unlock()
	var profile struct {
		Attributes []struct {
			Name string `json:"name"`
		} `json:"attributes"`
	}
	var declared map[string]bool
	if err := k.adminJSON(ctx, token, http.MethodGet, "/users/profile", nil, &profile); err == nil && len(profile.Attributes) > 0 {
		declared = map[string]bool{}
		for _, a := range profile.Attributes {
			declared[a.Name] = true
		}
	} else if err != nil {
		k.logger.Debug().Err(err).Msg("keycloak user profile not readable; writing the configured attributes")
	}
	k.mu.Lock()
	k.declared, k.declaredAt = declared, time.Now()
	k.mu.Unlock()
	return declared
}

// linkedOrOwnedUser returns the account to update: the linked ExternalID if
// it still exists, else an account with the username (or, in link mode, the
// email) that belongs to this person. "" means none exists. A username owned
// by someone else is a *UsernameTakenError.
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
	existingID, err := k.findUserID(ctx, token, "username", username)
	if err != nil {
		return "", err
	}
	if existingID == "" && k.cfg.LinkOnly && u.Email != "" {
		if existingID, err = k.findUserID(ctx, token, "email", u.Email); err != nil {
			return "", err
		}
	}
	if existingID == "" {
		return "", nil
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

// ErrLinkOnly is returned for account changes link mode never makes.
var ErrLinkOnly = errors.New("IDP_PROVISIONER_MODE=link: HR does not delete or rename accounts in this identity provider")

// DeleteAccount removes the user from the IdP (manage mode only).
func (k *KeycloakIdP) DeleteAccount(ctx context.Context, userID string) error {
	if k.cfg.LinkOnly {
		return ErrLinkOnly
	}
	return k.withFreshTokenRetry(func() error {
		token, err := k.accessToken(ctx)
		if err != nil {
			return err
		}
		return asAccountError(k.adminJSON(ctx, token, http.MethodDelete, "/users/"+url.PathEscape(userID), nil, nil))
	})
}

// SendPasswordSetup emails the user a new set-password link.
func (k *KeycloakIdP) SendPasswordSetup(ctx context.Context, userID string) error {
	return k.withFreshTokenRetry(func() error {
		token, err := k.accessToken(ctx)
		if err != nil {
			return err
		}
		return asAccountError(k.sendPasswordSetup(ctx, token, userID))
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

// EnsureGroups creates any missing HR groups (manage mode; link mode only
// checks that they exist).
func (k *KeycloakIdP) EnsureGroups(ctx context.Context, names []string) error {
	return k.withFreshTokenRetry(func() error { return k.ensureGroupsOnce(ctx, names) })
}

// pageSize is how many users or groups HR asks Keycloak for at once.
const pageSize = 100

type kcGroup struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Path          string    `json:"path"`
	SubGroupCount int       `json:"subGroupCount"`
	SubGroups     []kcGroup `json:"subGroups"`
}

// ListGroups returns the realm's groups (flattened; only those under the
// configured parent group) with all their direct members.
func (k *KeycloakIdP) ListGroups(ctx context.Context) ([]GroupInfo, error) {
	var out []GroupInfo
	err := k.withFreshTokenRetry(func() error {
		token, err := k.accessToken(ctx)
		if err != nil {
			return err
		}
		out = out[:0]
		var roots []kcGroup
		if k.cfg.GroupParent != "" {
			pid, err := k.groupIDByPath(ctx, token, k.cfg.GroupParent)
			if err != nil || pid == "" {
				return err
			}
			if roots, err = k.children(ctx, token, pid); err != nil {
				return err
			}
		} else {
			for first := 0; ; first += pageSize {
				var page []kcGroup
				if err := k.adminJSON(ctx, token, http.MethodGet, fmt.Sprintf("/groups?briefRepresentation=false&first=%d&max=%d", first, pageSize), nil, &page); err != nil {
					return err
				}
				roots = append(roots, page...)
				if len(page) < pageSize {
					break
				}
			}
		}
		var walk func(gs []kcGroup) error
		walk = func(gs []kcGroup) error {
			for _, g := range gs {
				members, err := k.members(ctx, token, g.ID)
				if err != nil {
					return err
				}
				name := strings.TrimPrefix(g.Name, k.cfg.GroupPrefix)
				out = append(out, GroupInfo{ID: g.ID, Name: name, Path: g.Path, Members: members})
				subs := g.SubGroups
				if len(subs) == 0 && g.SubGroupCount > 0 {
					if subs, err = k.children(ctx, token, g.ID); err != nil {
						return err
					}
				}
				if err := walk(subs); err != nil {
					return err
				}
			}
			return nil
		}
		return walk(roots)
	})
	return out, err
}

func (k *KeycloakIdP) children(ctx context.Context, token, groupID string) ([]kcGroup, error) {
	var out []kcGroup
	for first := 0; ; first += pageSize {
		var page []kcGroup
		path := fmt.Sprintf("/groups/%s/children?briefRepresentation=false&first=%d&max=%d", url.PathEscape(groupID), first, pageSize)
		if err := k.adminJSON(ctx, token, http.MethodGet, path, nil, &page); err != nil {
			return nil, err
		}
		out = append(out, page...)
		if len(page) < pageSize {
			return out, nil
		}
	}
}

// members pages through every direct member of a group.
func (k *KeycloakIdP) members(ctx context.Context, token, groupID string) ([]GroupMember, error) {
	out := []GroupMember{}
	for first := 0; ; first += pageSize {
		var page []struct {
			ID        string `json:"id"`
			Username  string `json:"username"`
			Email     string `json:"email"`
			FirstName string `json:"firstName"`
			LastName  string `json:"lastName"`
			Enabled   bool   `json:"enabled"`
		}
		path := fmt.Sprintf("/groups/%s/members?first=%d&max=%d", url.PathEscape(groupID), first, pageSize)
		if err := k.adminJSON(ctx, token, http.MethodGet, path, nil, &page); err != nil {
			return nil, err
		}
		for _, m := range page {
			out = append(out, GroupMember{ID: m.ID, Username: m.Username, Email: m.Email, FirstName: m.FirstName, LastName: m.LastName, Enabled: m.Enabled})
		}
		if len(page) < pageSize {
			return out, nil
		}
	}
}

// ScanUsers calls fn for every account in the realm, page by page (review
// queue: accounts without an hrPersonId).
func (k *KeycloakIdP) ScanUsers(ctx context.Context, fn func(UserInfo) error) error {
	token, err := k.accessToken(ctx)
	if err != nil {
		return err
	}
	for first := 0; ; first += pageSize {
		var page []struct {
			ID               string              `json:"id"`
			Username         string              `json:"username"`
			Email            string              `json:"email"`
			EmailVerified    bool                `json:"emailVerified"`
			FirstName        string              `json:"firstName"`
			LastName         string              `json:"lastName"`
			Enabled          bool                `json:"enabled"`
			CreatedTimestamp int64               `json:"createdTimestamp"`
			Attributes       map[string][]string `json:"attributes"`
		}
		path := fmt.Sprintf("/users?briefRepresentation=false&first=%d&max=%d", first, pageSize)
		if err := k.adminJSON(ctx, token, http.MethodGet, path, nil, &page); err != nil {
			if errors.Is(err, errUnauthorized) {
				k.mu.Lock()
				k.token = ""
				k.mu.Unlock()
			}
			return err
		}
		for _, u := range page {
			if strings.HasPrefix(u.Username, "service-account-") {
				continue
			}
			info := UserInfo{ID: u.ID, Username: u.Username, Email: u.Email, EmailVerified: u.EmailVerified,
				FirstName: u.FirstName, LastName: u.LastName, Enabled: u.Enabled, Attributes: map[string]string{}}
			for key, v := range u.Attributes {
				if len(v) > 0 {
					info.Attributes[key] = v[0]
				}
			}
			if u.CreatedTimestamp > 0 {
				t := time.UnixMilli(u.CreatedTimestamp).UTC()
				info.CreatedAt = &t
			}
			if err := fn(info); err != nil {
				return err
			}
		}
		if len(page) < pageSize {
			return nil
		}
	}
}

// UpdateMembership adds the user to `add` groups (creating missing ones in
// manage mode) and removes them from `remove` groups that exist.
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
			gid, err := k.groupIDByPath(ctx, token, k.GroupPath(name))
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
// when p.Username is set (needs editUsernameAllowed on the realm). In link
// mode only HR's own attribute changes.
func (k *KeycloakIdP) UpdateProfile(ctx context.Context, userID string, p Profile) error {
	return k.withFreshTokenRetry(func() error {
		token, err := k.accessToken(ctx)
		if err != nil {
			return err
		}
		attrs := k.allowedAttributes(ctx, token, p.Attributes)
		if k.cfg.LinkOnly {
			if v, ok := attrs[AttrHRPersonID]; ok {
				return k.patchUserOnce(ctx, token, userID, map[string]any{}, map[string]string{AttrHRPersonID: v})
			}
			return nil
		}
		changes := map[string]any{"email": p.Email, "firstName": p.FirstName, "lastName": p.LastName}
		if u := strings.TrimSpace(p.Username); u != "" {
			changes["username"] = u
		}
		return k.patchUserOnce(ctx, token, userID, changes, attrs)
	})
}

// UsernameOwner looks the username up in the realm.
func (k *KeycloakIdP) UsernameOwner(ctx context.Context, username string) (string, bool, error) {
	owner, found := "", false
	err := k.withFreshTokenRetry(func() error {
		token, err := k.accessToken(ctx)
		if err != nil {
			return err
		}
		id, err := k.findUserID(ctx, token, "username", username)
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
		if _, err := k.resolveGroup(ctx, token, name); err != nil {
			return err
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

// findUserID looks a user up by exact username or email.
func (k *KeycloakIdP) findUserID(ctx context.Context, token, field, value string) (string, error) {
	q := url.Values{}
	q.Set(field, value)
	q.Set("exact", "true")
	var users []struct {
		ID string `json:"id"`
	}
	if err := k.adminJSON(ctx, token, http.MethodGet, "/users?"+q.Encode(), nil, &users); err != nil {
		return "", err
	}
	if len(users) != 1 {
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
		username, _ := payload["username"].(string)
		return k.findUserID(ctx, token, "username", username)
	}
	parts := strings.Split(strings.TrimRight(loc, "/"), "/")
	return parts[len(parts)-1], nil
}

func (k *KeycloakIdP) syncGroups(ctx context.Context, token, userID string, groups []string) ([]string, error) {
	applied := make([]string, 0, len(groups))
	for _, name := range uniqueNonEmpty(groups) {
		gid, err := k.resolveGroup(ctx, token, name)
		if err != nil {
			return applied, err
		}
		path := "/users/" + url.PathEscape(userID) + "/groups/" + url.PathEscape(gid)
		if err := k.adminJSON(ctx, token, http.MethodPut, path, map[string]any{}, nil); err != nil {
			return applied, err
		}
		applied = append(applied, name)
	}
	return applied, nil
}

// resolveGroup finds an HR group by its full path, creating it (and only
// it) in manage mode. HR only passes groups it validated against the module
// catalogs or its departments.
func (k *KeycloakIdP) resolveGroup(ctx context.Context, token, name string) (string, error) {
	path := k.GroupPath(name)
	gid, err := k.groupIDByPath(ctx, token, path)
	if err != nil || gid != "" {
		return gid, err
	}
	if k.cfg.LinkOnly {
		return "", fmt.Errorf("keycloak group %s does not exist; ask the identity provider's administrators to create it", path)
	}
	return k.createGroup(ctx, token, path)
}

// groupIDByPath looks a group up by its full path ("" when missing).
func (k *KeycloakIdP) groupIDByPath(ctx context.Context, token, path string) (string, error) {
	k.mu.Lock()
	if id, ok := k.groupCache[path]; ok {
		k.mu.Unlock()
		return id, nil
	}
	k.mu.Unlock()
	segs := strings.Split(strings.Trim(path, "/"), "/")
	for i := range segs {
		segs[i] = url.PathEscape(segs[i])
	}
	var g struct {
		ID string `json:"id"`
	}
	err := k.adminJSON(ctx, token, http.MethodGet, "/group-by-path/"+strings.Join(segs, "/"), nil, &g)
	var ae *AdminError
	if errors.As(err, &ae) && ae.Status == http.StatusNotFound {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	k.mu.Lock()
	k.groupCache[path] = g.ID
	k.mu.Unlock()
	return g.ID, nil
}

// createGroup adds the group at path (its parent must exist).
func (k *KeycloakIdP) createGroup(ctx context.Context, token, path string) (string, error) {
	parentPath, name := "", strings.TrimPrefix(path, "/")
	if i := strings.LastIndex(path, "/"); i > 0 {
		parentPath, name = path[:i], path[i+1:]
	}
	endpoint := "/groups"
	if parentPath != "" {
		pid, err := k.groupIDByPath(ctx, token, parentPath)
		if err != nil {
			return "", err
		}
		if pid == "" {
			return "", fmt.Errorf("keycloak parent group %s does not exist; create it or fix KEYCLOAK_GROUP_PARENT", parentPath)
		}
		endpoint = "/groups/" + url.PathEscape(pid) + "/children"
	}
	res, err := k.adminRaw(ctx, token, http.MethodPost, endpoint, map[string]any{"name": name})
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusCreated && res.StatusCode != http.StatusConflict {
		return "", fmt.Errorf("keycloak create group %s: status %d: %s", path, res.StatusCode, truncate(string(body), 300))
	}
	if loc := res.Header.Get("Location"); loc != "" {
		parts := strings.Split(strings.TrimRight(loc, "/"), "/")
		id := parts[len(parts)-1]
		k.mu.Lock()
		k.groupCache[path] = id
		k.mu.Unlock()
		k.logger.Info().Str("group", path).Msg("keycloak group created")
		return id, nil
	}
	id, err := k.groupIDByPath(ctx, token, path)
	if err != nil {
		return "", err
	}
	if id == "" {
		return "", fmt.Errorf("keycloak group %s could not be created", path)
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
