package provisioners

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/AGlElectronics/hr/backend/internal/gworkspace"
)

// GoogleWorkspace is a WorkspaceDirectory backed by the shared Google Admin
// SDK client (gworkspace).
type GoogleWorkspace struct{ dir gworkspace.Directory }

// NewGoogleWorkspace wraps a gworkspace directory (the real client or
// gworkspace.NewFake in tests).
func NewGoogleWorkspace(dir gworkspace.Directory) *GoogleWorkspace { return &GoogleWorkspace{dir: dir} }

// NewGoogleWorkspaceFactory opens the shared client with the service-account
// key in credentialsFile (GOOGLE_WORKSPACE_CREDENTIALS_FILE), acting as the
// target's admin in the target's domain (HR settings). Clients are reused
// per admin and domain, so access tokens are cached.
func NewGoogleWorkspaceFactory(credentialsFile string, opts gworkspace.Options) WorkspaceFactory {
	var mu sync.Mutex
	clients := map[string]*GoogleWorkspace{}
	return func(_ context.Context, t WorkspaceTarget) (WorkspaceDirectory, error) {
		cfg := gworkspace.Config{CredentialsFile: credentialsFile, AdminEmail: strings.ToLower(t.AdminEmail), Domain: strings.ToLower(t.Domain)}
		key := cfg.AdminEmail + " " + cfg.Domain
		mu.Lock()
		defer mu.Unlock()
		if c, ok := clients[key]; ok {
			return c, nil
		}
		c, err := gworkspace.New(cfg, opts)
		if err != nil {
			return nil, fmt.Errorf("google workspace: %w", err)
		}
		clients[key] = NewGoogleWorkspace(c)
		return clients[key], nil
	}
}

func mapWorkspaceErr(err error) error {
	if gworkspace.IsNotFound(err) {
		return fmt.Errorf("%w: %v", ErrWorkspaceNotFound, err)
	}
	return err
}

func (g *GoogleWorkspace) GetUser(ctx context.Context, email string) (*WorkspaceUser, error) {
	u, err := g.dir.GetUser(ctx, email)
	if err != nil {
		return nil, mapWorkspaceErr(err)
	}
	return &WorkspaceUser{PrimaryEmail: u.PrimaryEmail, GivenName: u.Name.GivenName, FamilyName: u.Name.FamilyName,
		OrgUnitPath: u.OrgUnitPath, RecoveryEmail: u.RecoveryEmail, Suspended: u.Suspended}, nil
}

func (g *GoogleWorkspace) CreateUser(ctx context.Context, u WorkspaceUser) error {
	_, err := g.dir.CreateUser(ctx, gworkspace.NewUser{PrimaryEmail: u.PrimaryEmail, GivenName: u.GivenName,
		FamilyName: u.FamilyName, OrgUnitPath: u.OrgUnitPath, RecoveryEmail: u.RecoveryEmail})
	return mapWorkspaceErr(err)
}

func (g *GoogleWorkspace) SetSuspended(ctx context.Context, email string, suspended bool) error {
	_, err := g.dir.SetSuspended(ctx, email, suspended)
	return mapWorkspaceErr(err)
}

func (g *GoogleWorkspace) DeleteUser(ctx context.Context, email string) error {
	return mapWorkspaceErr(g.dir.DeleteUser(ctx, email))
}

// EnsureGroup creates the group unless it exists.
func (g *GoogleWorkspace) EnsureGroup(ctx context.Context, email, name string) error {
	if _, err := g.dir.GetGroup(ctx, email); err == nil || !gworkspace.IsNotFound(err) {
		return err
	}
	if _, err := g.dir.CreateGroup(ctx, gworkspace.Group{Email: email, Name: name}); err != nil && !gworkspace.IsConflict(err) {
		return err
	}
	return nil
}

func (g *GoogleWorkspace) ListMembers(ctx context.Context, group string) ([]string, error) {
	members, err := g.dir.ListMembers(ctx, group)
	if err != nil {
		return nil, mapWorkspaceErr(err)
	}
	out := make([]string, 0, len(members))
	for _, m := range members {
		if m.Type == "" || m.Type == gworkspace.TypeUser {
			out = append(out, strings.ToLower(m.Email))
		}
	}
	return out, nil
}

// AddMember adds a person; already being a member is fine.
func (g *GoogleWorkspace) AddMember(ctx context.Context, group, member string) error {
	_, err := g.dir.AddMember(ctx, group, gworkspace.Member{Email: member, Role: gworkspace.RoleMember})
	if gworkspace.IsConflict(err) {
		return nil
	}
	return mapWorkspaceErr(err)
}

// RemoveMember removes a person; not being a member is fine.
func (g *GoogleWorkspace) RemoveMember(ctx context.Context, group, member string) error {
	err := g.dir.RemoveMember(ctx, group, member)
	if gworkspace.IsNotFound(err) {
		return nil
	}
	return err
}
