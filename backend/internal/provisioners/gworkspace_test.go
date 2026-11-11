package provisioners

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/AGlElectronics/hr/backend/internal/gworkspace"
)

func TestGoogleWorkspaceAdapter(t *testing.T) {
	ctx := context.Background()
	var ws WorkspaceDirectory = NewGoogleWorkspace(gworkspace.NewFake("example.org"))

	if _, err := ws.GetUser(ctx, "ada@example.org"); !errors.Is(err, ErrWorkspaceNotFound) {
		t.Fatalf("missing user: %v", err)
	}
	if err := ws.CreateUser(ctx, WorkspaceUser{PrimaryEmail: "ada@example.org", GivenName: "Ada", FamilyName: "L", OrgUnitPath: "/"}); err != nil {
		t.Fatal(err)
	}
	if err := ws.SetSuspended(ctx, "ada@example.org", true); err != nil {
		t.Fatal(err)
	}
	if u, err := ws.GetUser(ctx, "ada@example.org"); err != nil || !u.Suspended || u.GivenName != "Ada" {
		t.Fatalf("user: %+v %v", u, err)
	}

	for i := 0; i < 2; i++ {
		if err := ws.EnsureGroup(ctx, "dept-security@example.org", "Security"); err != nil {
			t.Fatalf("ensure group (run %d): %v", i, err)
		}
		if err := ws.AddMember(ctx, "dept-security@example.org", "ada@example.org"); err != nil {
			t.Fatalf("add member (run %d): %v", i, err)
		}
	}
	if m, err := ws.ListMembers(ctx, "dept-security@example.org"); err != nil || len(m) != 1 || m[0] != "ada@example.org" {
		t.Fatalf("members: %v %v", m, err)
	}
	for i := 0; i < 2; i++ {
		if err := ws.RemoveMember(ctx, "dept-security@example.org", "ada@example.org"); err != nil {
			t.Fatalf("remove member (run %d): %v", i, err)
		}
	}
	if err := ws.DeleteUser(ctx, "ada@example.org"); err != nil {
		t.Fatal(err)
	}
	if err := ws.DeleteUser(ctx, "ada@example.org"); !errors.Is(err, ErrWorkspaceNotFound) {
		t.Fatalf("second delete: %v", err)
	}
}

func TestGoogleWorkspaceFactoryNeedsCredentials(t *testing.T) {
	f := NewGoogleWorkspaceFactory("does-not-exist.json", gworkspace.Options{})
	if _, err := f(context.Background(), WorkspaceTarget{Domain: "example.org", AdminEmail: "admin@example.org"}); err == nil {
		t.Fatal("a missing key file must fail")
	}
	if _, err := f(context.Background(), WorkspaceTarget{Domain: "example.org", AdminEmail: "admin@other.org"}); err == nil {
		t.Fatal("an admin outside the domain must fail")
	}
}

// googleREST is a tiny stand-in for the Admin SDK Directory API. Tests never
// call Google.
type googleREST struct {
	mu      sync.Mutex
	users   map[string]map[string]any
	groups  map[string]map[string]any
	members map[string]map[string]bool
}

func (g *googleREST) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	path := strings.TrimPrefix(r.URL.Path, "/admin/directory/v1")
	unesc := func(s string) string {
		u, err := url.PathUnescape(s)
		if err != nil {
			return s
		}
		return u
	}
	write := func(status int, body any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if body != nil {
			_ = json.NewEncoder(w).Encode(body)
		}
	}
	notFound := func() {
		write(http.StatusNotFound, map[string]any{"error": map[string]any{"message": "Resource Not Found", "errors": []any{map[string]any{"reason": "notFound"}}}})
	}
	var body map[string]any
	if r.Body != nil && r.ContentLength != 0 {
		raw, _ := io.ReadAll(r.Body)
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &body)
		}
	}
	switch {
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/users/"):
		email := unesc(strings.TrimPrefix(path, "/users/"))
		u, ok := g.users[email]
		if !ok {
			notFound()
			return
		}
		write(http.StatusOK, u)
	case r.Method == http.MethodPost && path == "/users":
		email, _ := body["primaryEmail"].(string)
		if _, ok := g.users[email]; ok {
			write(http.StatusConflict, map[string]any{"error": map[string]any{"message": "Entity already exists"}})
			return
		}
		if _, ok := body["password"]; !ok {
			tFail(w, "create user without a password")
			return
		}
		delete(body, "password")
		g.users[email] = body
		write(http.StatusOK, body)
	case r.Method == http.MethodPut && strings.HasPrefix(path, "/users/"):
		email := unesc(strings.TrimPrefix(path, "/users/"))
		u, ok := g.users[email]
		if !ok {
			notFound()
			return
		}
		for k, v := range body {
			u[k] = v
		}
		write(http.StatusOK, u)
	case r.Method == http.MethodDelete && strings.HasPrefix(path, "/users/"):
		email := unesc(strings.TrimPrefix(path, "/users/"))
		if _, ok := g.users[email]; !ok {
			notFound()
			return
		}
		delete(g.users, email)
		write(http.StatusNoContent, nil)
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/groups/") && strings.HasSuffix(path, "/members"):
		email := unesc(strings.TrimSuffix(strings.TrimPrefix(path, "/groups/"), "/members"))
		if _, ok := g.groups[email]; !ok {
			notFound()
			return
		}
		var members []map[string]any
		for m := range g.members[email] {
			members = append(members, map[string]any{"email": m, "type": "USER", "role": "MEMBER"})
		}
		write(http.StatusOK, map[string]any{"members": members})
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/members"):
		email := unesc(strings.TrimSuffix(strings.TrimPrefix(path, "/groups/"), "/members"))
		member, _ := body["email"].(string)
		if g.members[email][member] {
			write(http.StatusConflict, map[string]any{"error": map[string]any{"message": "Member already exists"}})
			return
		}
		if g.members[email] == nil {
			g.members[email] = map[string]bool{}
		}
		g.members[email][member] = true
		write(http.StatusOK, body)
	case r.Method == http.MethodDelete && strings.Contains(path, "/members/"):
		rest := strings.TrimPrefix(path, "/groups/")
		group, member, _ := strings.Cut(rest, "/members/")
		group, member = unesc(group), unesc(member)
		if !g.members[group][member] {
			notFound()
			return
		}
		delete(g.members[group], member)
		write(http.StatusNoContent, nil)
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/groups/"):
		email := unesc(strings.TrimPrefix(path, "/groups/"))
		gr, ok := g.groups[email]
		if !ok {
			notFound()
			return
		}
		write(http.StatusOK, gr)
	case r.Method == http.MethodPost && path == "/groups":
		email, _ := body["email"].(string)
		g.groups[email] = body
		if g.members[email] == nil {
			g.members[email] = map[string]bool{}
		}
		write(http.StatusOK, body)
	default:
		write(http.StatusNotFound, map[string]any{"error": map[string]any{"message": "no such route " + r.Method + " " + path}})
	}
}

func tFail(w http.ResponseWriter, msg string) {
	http.Error(w, msg, http.StatusBadRequest)
}

func TestGoogleWorkspaceAgainstDirectoryAPI(t *testing.T) {
	api := &googleREST{users: map[string]map[string]any{}, groups: map[string]map[string]any{}, members: map[string]map[string]bool{}}
	srv := httptest.NewServer(api)
	defer srv.Close()

	client, err := gworkspace.New(gworkspace.Config{
		CredentialsFile: "unused-test-key.json",
		AdminEmail:      "admin@example.org",
		Domain:          "example.org",
	}, gworkspace.Options{
		DirectoryURL: srv.URL + "/admin/directory/v1",
		HTTPClient: func(context.Context, string, []string) (*http.Client, error) {
			return srv.Client(), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ws := NewGoogleWorkspace(client)
	ctx := context.Background()

	if err := ws.CreateUser(ctx, WorkspaceUser{
		PrimaryEmail: "ada@example.org", GivenName: "Ada", FamilyName: "Lovelace",
		OrgUnitPath: "/Volunteers", RecoveryEmail: "ada@home.test",
	}); err != nil {
		t.Fatal(err)
	}
	api.mu.Lock()
	created := api.users["ada@example.org"]
	_, passwordLeft := created["password"]
	recovery, _ := created["recoveryEmail"].(string)
	ou, _ := created["orgUnitPath"].(string)
	api.mu.Unlock()
	if passwordLeft || recovery != "ada@home.test" || ou != "/Volunteers" {
		t.Fatalf("create body: password left=%v recovery=%q ou=%q", passwordLeft, recovery, ou)
	}
	if err := ws.SetSuspended(ctx, "ada@example.org", true); err != nil {
		t.Fatal(err)
	}
	u, err := ws.GetUser(ctx, "ada@example.org")
	if err != nil || !u.Suspended || u.RecoveryEmail != "ada@home.test" || u.GivenName != "Ada" {
		t.Fatalf("user: %+v %v", u, err)
	}
	if err := ws.EnsureGroup(ctx, "dept-security@example.org", "Security"); err != nil {
		t.Fatal(err)
	}
	if err := ws.EnsureGroup(ctx, "dept-security@example.org", "Security"); err != nil {
		t.Fatalf("second ensure: %v", err)
	}
	if err := ws.AddMember(ctx, "dept-security@example.org", "ada@example.org"); err != nil {
		t.Fatal(err)
	}
	if err := ws.AddMember(ctx, "dept-security@example.org", "ada@example.org"); err != nil {
		t.Fatalf("second add: %v", err)
	}
	members, err := ws.ListMembers(ctx, "dept-security@example.org")
	if err != nil || len(members) != 1 || members[0] != "ada@example.org" {
		t.Fatalf("members: %v %v", members, err)
	}
	if err := ws.RemoveMember(ctx, "dept-security@example.org", "ada@example.org"); err != nil {
		t.Fatal(err)
	}
	if err := ws.RemoveMember(ctx, "dept-security@example.org", "ada@example.org"); err != nil {
		t.Fatalf("second remove: %v", err)
	}
	if err := ws.DeleteUser(ctx, "ada@example.org"); err != nil {
		t.Fatal(err)
	}
	if err := ws.DeleteUser(ctx, "ada@example.org"); !errors.Is(err, ErrWorkspaceNotFound) {
		t.Fatalf("second delete: %v", err)
	}
}
