package provisioners

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

// fakeKeycloak is a small in-memory Keycloak Admin API.
type fakeKeycloak struct {
	t  *testing.T
	mu sync.Mutex

	users    map[string]map[string]any // id -> representation
	groups   map[string]string         // path -> id
	members  map[string][]string       // group id -> user ids
	declared []string                  // user profile attributes

	tokens       int
	rejectToken  string // a token the admin API answers 401 for
	setupMails   []string
	failSetup    bool
	passwordSets int
	nextID       int
}

func newFakeKeycloak(t *testing.T) (*fakeKeycloak, *httptest.Server) {
	f := &fakeKeycloak{t: t, users: map[string]map[string]any{}, groups: map[string]string{}, members: map[string][]string{},
		declared: []string{"username", "email", "firstName", "lastName", AttrHRPersonID, AttrNickname, AttrOrgEmail, AttrPhone}}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakeKeycloak) id(prefix string) string {
	f.nextID++
	return fmt.Sprintf("%s-%d", prefix, f.nextID)
}

func (f *fakeKeycloak) addGroup(path string) string {
	id := f.id("g")
	f.groups[path] = id
	return id
}

func (f *fakeKeycloak) addUser(username, email string, attrs map[string][]string) string {
	id := f.id("u")
	f.users[id] = map[string]any{"id": id, "username": username, "email": email, "enabled": true, "attributes": attrs}
	return id
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func page[T any](items []T, q url.Values) []T {
	first, _ := strconv.Atoi(q.Get("first"))
	max, err := strconv.Atoi(q.Get("max"))
	if err != nil {
		max = 100
	}
	if first > len(items) {
		return []T{}
	}
	end := first + max
	if end > len(items) {
		end = len(items)
	}
	return items[first:end]
}

func (f *fakeKeycloak) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if strings.HasSuffix(r.URL.Path, "/protocol/openid-connect/token") {
		f.tokens++
		writeJSON(w, map[string]any{"access_token": fmt.Sprintf("tok-%d", f.tokens), "expires_in": 300})
		return
	}
	if r.Header.Get("Authorization") == "Bearer "+f.rejectToken {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/admin/realms/test")
	segs := strings.Split(strings.Trim(path, "/"), "/")
	q := r.URL.Query()
	switch {
	case path == "/users/profile":
		attrs := []map[string]string{}
		for _, a := range f.declared {
			attrs = append(attrs, map[string]string{"name": a})
		}
		writeJSON(w, map[string]any{"attributes": attrs})
	case path == "/users" && r.Method == http.MethodGet:
		out := []map[string]any{}
		for _, u := range f.users {
			switch {
			case q.Get("username") != "":
				if u["username"] == q.Get("username") {
					out = append(out, u)
				}
			case q.Get("email") != "":
				if strings.EqualFold(fmt.Sprint(u["email"]), q.Get("email")) {
					out = append(out, u)
				}
			default:
				out = append(out, u)
			}
		}
		writeJSON(w, page(out, q))
	case path == "/users" && r.Method == http.MethodPost:
		var rep map[string]any
		_ = json.NewDecoder(r.Body).Decode(&rep)
		id := f.id("u")
		rep["id"] = id
		f.users[id] = rep
		w.Header().Set("Location", "http://kc/admin/realms/test/users/"+id)
		w.WriteHeader(http.StatusCreated)
	case len(segs) >= 2 && segs[0] == "users":
		u, ok := f.users[segs[1]]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		switch {
		case len(segs) == 2 && r.Method == http.MethodGet:
			writeJSON(w, u)
		case len(segs) == 2 && r.Method == http.MethodPut:
			var rep map[string]any
			_ = json.NewDecoder(r.Body).Decode(&rep)
			f.users[segs[1]] = rep
			w.WriteHeader(http.StatusNoContent)
		case len(segs) == 2 && r.Method == http.MethodDelete:
			delete(f.users, segs[1])
			w.WriteHeader(http.StatusNoContent)
		case len(segs) == 4 && segs[2] == "groups" && r.Method == http.MethodPut:
			f.members[segs[3]] = append(f.members[segs[3]], segs[1])
			w.WriteHeader(http.StatusNoContent)
		case len(segs) == 4 && segs[2] == "groups" && r.Method == http.MethodDelete:
			kept := []string{}
			for _, m := range f.members[segs[3]] {
				if m != segs[1] {
					kept = append(kept, m)
				}
			}
			f.members[segs[3]] = kept
			w.WriteHeader(http.StatusNoContent)
		case len(segs) == 3 && segs[2] == "execute-actions-email":
			if f.failSetup {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"errorMessage":"Failed to send execute actions email"}`))
				return
			}
			f.setupMails = append(f.setupMails, segs[1])
			w.WriteHeader(http.StatusNoContent)
		case len(segs) == 3 && segs[2] == "reset-password":
			f.passwordSets++
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	case segs[0] == "group-by-path":
		p := "/" + strings.Join(segs[1:], "/")
		if id, ok := f.groups[p]; ok {
			writeJSON(w, map[string]string{"id": id, "path": p})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	case path == "/groups" && r.Method == http.MethodPost:
		var rep map[string]string
		_ = json.NewDecoder(r.Body).Decode(&rep)
		id := f.addGroup("/" + rep["name"])
		w.Header().Set("Location", "http://kc/groups/"+id)
		w.WriteHeader(http.StatusCreated)
	case path == "/groups" && r.Method == http.MethodGet:
		out := []map[string]any{}
		for p, id := range f.groups {
			if strings.Count(p, "/") == 1 {
				out = append(out, map[string]any{"id": id, "name": strings.TrimPrefix(p, "/"), "path": p, "subGroupCount": 0})
			}
		}
		writeJSON(w, page(out, q))
	case len(segs) == 3 && segs[0] == "groups" && segs[2] == "children" && r.Method == http.MethodPost:
		var rep map[string]string
		_ = json.NewDecoder(r.Body).Decode(&rep)
		parent := ""
		for p, id := range f.groups {
			if id == segs[1] {
				parent = p
			}
		}
		id := f.addGroup(parent + "/" + rep["name"])
		w.Header().Set("Location", "http://kc/groups/"+id)
		w.WriteHeader(http.StatusCreated)
	case len(segs) == 3 && segs[0] == "groups" && segs[2] == "children":
		parent := ""
		for p, id := range f.groups {
			if id == segs[1] {
				parent = p
			}
		}
		out := []map[string]any{}
		for p, id := range f.groups {
			if strings.HasPrefix(p, parent+"/") && !strings.Contains(strings.TrimPrefix(p, parent+"/"), "/") {
				out = append(out, map[string]any{"id": id, "name": p[strings.LastIndex(p, "/")+1:], "path": p})
			}
		}
		writeJSON(w, page(out, q))
	case len(segs) == 3 && segs[0] == "groups" && segs[2] == "members":
		out := []map[string]any{}
		for _, id := range f.members[segs[1]] {
			out = append(out, map[string]any{"id": id, "username": f.users[id]["username"], "enabled": true})
		}
		writeJSON(w, page(out, q))
	default:
		f.t.Errorf("fake keycloak: unexpected %s %s", r.Method, r.URL.String())
		w.WriteHeader(http.StatusNotFound)
	}
}

func newTestIDP(t *testing.T, srv *httptest.Server, mutate func(*KeycloakConfig)) *KeycloakIDP {
	t.Helper()
	cfg := KeycloakConfig{BaseURL: srv.URL, Realm: "test", ClientID: "hr-provisioner", ClientSecret: "secret"}
	if mutate != nil {
		mutate(&cfg)
	}
	k, err := NewKeycloakIDP(cfg, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func attrsOf(u map[string]any) map[string]any {
	a, _ := u["attributes"].(map[string]any)
	return a
}

func TestKeycloakProvisionManageMode(t *testing.T) {
	kc, srv := newFakeKeycloak(t)
	kc.addGroup("/conservices")
	idp := newTestIDP(t, srv, func(c *KeycloakConfig) { c.GroupParent = "conservices/"; c.GroupPrefix = "cs-" })
	person := uuid.New()

	res, err := idp.Provision(context.Background(), IDPUser{
		HRPersonID: person, Username: "ada", Email: "ada@example.org", FirstName: "Ada", LastName: "Lovelace",
		Attributes:        map[string]string{AttrNickname: "ada", AttrPhone: "+441", AttrPrivateEmail: "ada@example.org", AttrOrgEmail: "ada@con.org"},
		Groups:            []string{"dept-security", "stash-volunteer"},
		SendPasswordSetup: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Created || !res.PasswordSetupEmailSent || len(res.GroupsApplied) != 2 {
		t.Fatalf("result %+v", res)
	}
	u := kc.users[res.ExternalID]
	attrs := attrsOf(u)
	if attrs == nil {
		raw, _ := json.Marshal(u["attributes"])
		_ = json.Unmarshal(raw, &attrs)
	}
	if _, ok := attrs[AttrPhone]; ok {
		t.Errorf("phone is not in the default attribute list but was written: %v", attrs)
	}
	if _, ok := attrs[AttrPrivateEmail]; ok {
		t.Errorf("privateEmail was written: %v", attrs)
	}
	if _, ok := attrs[AttrHRPersonID]; !ok {
		t.Errorf("hrPersonId missing: %v", attrs)
	}
	for _, p := range []string{"/conservices/cs-dept-security", "/conservices/cs-stash-volunteer"} {
		if _, ok := kc.groups[p]; !ok {
			t.Errorf("group %s not created under the parent with the prefix (groups: %v)", p, kc.groups)
		}
	}
	if kc.passwordSets != 0 {
		t.Error("HR set a password")
	}
}

func TestKeycloakSetupEmailFailureSetsNoPassword(t *testing.T) {
	kc, srv := newFakeKeycloak(t)
	kc.failSetup = true
	idp := newTestIDP(t, srv, nil)
	res, err := idp.Provision(context.Background(), IDPUser{HRPersonID: uuid.New(), Username: "bob", Email: "bob@example.org", SendPasswordSetup: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.PasswordSetupEmailSent || res.PasswordSetupError == "" || kc.passwordSets != 0 {
		t.Fatalf("failed setup email must not fall back to a password: %+v, password sets %d", res, kc.passwordSets)
	}
	kc.failSetup = false
	if err := idp.SendPasswordSetup(context.Background(), res.ExternalID); err != nil || len(kc.setupMails) != 1 {
		t.Fatalf("resend: %v %v", err, kc.setupMails)
	}
}

func TestKeycloakDeclaredAttributesOnly(t *testing.T) {
	kc, srv := newFakeKeycloak(t)
	kc.declared = []string{AttrHRPersonID}
	idp := newTestIDP(t, srv, func(c *KeycloakConfig) { c.Attributes = []string{AttrHRPersonID, AttrNickname} })
	res, err := idp.Provision(context.Background(), IDPUser{HRPersonID: uuid.New(), Username: "cy", Email: "cy@example.org",
		Attributes: map[string]string{AttrNickname: "cy"}})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(kc.users[res.ExternalID]["attributes"])
	if strings.Contains(string(raw), AttrNickname) {
		t.Fatalf("nickname is not declared in the realm but was written: %s", raw)
	}
}

func TestKeycloakLinkMode(t *testing.T) {
	kc, srv := newFakeKeycloak(t)
	idp := newTestIDP(t, srv, func(c *KeycloakConfig) { c.LinkOnly = true; c.GroupParent = "/con" })
	person := uuid.New()
	ctx := context.Background()

	_, err := idp.Provision(ctx, IDPUser{HRPersonID: person, Username: "dora", Email: "dora@example.org", Groups: []string{"dept-x"}})
	if !errors.Is(err, ErrNoAccount) {
		t.Fatalf("no account in link mode: want ErrNoAccount, got %v", err)
	}
	if len(kc.users) != 0 {
		t.Fatal("link mode created an account")
	}

	existing := kc.addUser("d.explorer", "dora@example.org", map[string][]string{})
	if _, err := idp.Provision(ctx, IDPUser{HRPersonID: person, Username: "dora", Email: "dora@example.org", Groups: []string{"dept-x"}}); err == nil ||
		!strings.Contains(err.Error(), "/con/dept-x does not exist") {
		t.Fatalf("missing group in link mode: want error, got %v", err)
	}
	kc.addGroup("/con")
	kc.addGroup("/con/dept-x")
	res, err := idp.Provision(ctx, IDPUser{HRPersonID: person, Username: "dora", Email: "dora@example.org", FirstName: "Changed", Groups: []string{"dept-x"}, SendPasswordSetup: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExternalID != existing || res.Created || res.PasswordSetupEmailSent {
		t.Fatalf("link mode should link the existing account by email: %+v", res)
	}
	if kc.users[existing]["firstName"] == "Changed" || kc.users[existing]["username"] != "d.explorer" {
		t.Fatalf("link mode changed the convention's account: %v", kc.users[existing])
	}
	if err := idp.DeleteAccount(ctx, existing); !errors.Is(err, ErrLinkOnly) {
		t.Fatalf("delete in link mode: want ErrLinkOnly, got %v", err)
	}
}

func TestKeycloakListGroupsPagesMembers(t *testing.T) {
	kc, srv := newFakeKeycloak(t)
	gid := kc.addGroup("/stash-volunteer")
	for i := 0; i < 250; i++ {
		kc.members[gid] = append(kc.members[gid], kc.addUser(fmt.Sprintf("v%d", i), "", nil))
	}
	idp := newTestIDP(t, srv, nil)
	groups, err := idp.ListGroups(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || len(groups[0].Members) != 250 {
		t.Fatalf("want 1 group with 250 members, got %d groups (%v)", len(groups), groups)
	}
	n := 0
	if err := idp.ScanUsers(context.Background(), func(UserInfo) error { n++; return nil }); err != nil || n != 250 {
		t.Fatalf("ScanUsers saw %d users (%v)", n, err)
	}
}

func TestKeycloakRetriesWithFreshToken(t *testing.T) {
	kc, srv := newFakeKeycloak(t)
	id := kc.addUser("eve", "eve@example.org", nil)
	idp := newTestIDP(t, srv, nil)
	if _, err := idp.GetAccount(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	kc.mu.Lock()
	kc.rejectToken = "tok-1"
	kc.mu.Unlock()
	info, err := idp.GetAccount(context.Background(), id)
	if err != nil || info.Username != "eve" || kc.tokens != 2 {
		t.Fatalf("after a rejected token: %v %v (tokens %d)", info, err, kc.tokens)
	}
	if _, err := idp.GetAccount(context.Background(), "nobody"); !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("missing user: want ErrAccountNotFound, got %v", err)
	}
}
