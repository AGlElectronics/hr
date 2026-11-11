package provisioners

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
)

// WorkspaceUser is a Google Workspace account as HR sees it.
type WorkspaceUser struct {
	PrimaryEmail string
	GivenName    string
	FamilyName   string
	OrgUnitPath  string
	// RecoveryEmail is the person's private address, used by Google's
	// account recovery to let them set their own password.
	RecoveryEmail string
	Suspended     bool
}

// ErrWorkspaceNotFound means the Workspace user or group does not exist.
var ErrWorkspaceNotFound = errors.New("not found in Google Workspace")

// WorkspaceDirectory is the part of the Google Admin SDK Directory API HR
// needs. The real client (shared/go/gworkspace) is plugged in later; HR's
// logic runs against this interface.
type WorkspaceDirectory interface {
	GetUser(ctx context.Context, email string) (*WorkspaceUser, error)
	// CreateUser creates the account with a random password the client
	// never returns; the person sets their own through account recovery.
	CreateUser(ctx context.Context, u WorkspaceUser) error
	SetSuspended(ctx context.Context, email string, suspended bool) error
	DeleteUser(ctx context.Context, email string) error
	EnsureGroup(ctx context.Context, email, name string) error
	ListMembers(ctx context.Context, groupEmail string) ([]string, error)
	AddMember(ctx context.Context, groupEmail, memberEmail string) error
	RemoveMember(ctx context.Context, groupEmail, memberEmail string) error
}

// WorkspaceTarget is where the directory acts: the Workspace domain, the
// admin it impersonates (domain-wide delegation) and the org unit for new
// accounts.
type WorkspaceTarget struct {
	Domain     string
	AdminEmail string
	OrgUnit    string
}

// WorkspaceFactory opens a directory for a target; nil means HR has no
// Workspace client (the connector is unavailable).
type WorkspaceFactory func(ctx context.Context, t WorkspaceTarget) (WorkspaceDirectory, error)

// FakeWorkspace is an in-memory WorkspaceDirectory for tests.
type FakeWorkspace struct {
	mu      sync.Mutex
	Users   map[string]*WorkspaceUser
	Groups  map[string][]string
	Deleted []string
}

// NewFakeWorkspace returns an empty FakeWorkspace.
func NewFakeWorkspace() *FakeWorkspace {
	return &FakeWorkspace{Users: map[string]*WorkspaceUser{}, Groups: map[string][]string{}}
}

func (f *FakeWorkspace) GetUser(_ context.Context, email string) (*WorkspaceUser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.Users[strings.ToLower(email)]
	if !ok {
		return nil, ErrWorkspaceNotFound
	}
	c := *u
	return &c, nil
}

func (f *FakeWorkspace) CreateUser(_ context.Context, u WorkspaceUser) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Users[strings.ToLower(u.PrimaryEmail)] = &u
	return nil
}

func (f *FakeWorkspace) SetSuspended(_ context.Context, email string, suspended bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.Users[strings.ToLower(email)]
	if !ok {
		return ErrWorkspaceNotFound
	}
	u.Suspended = suspended
	return nil
}

func (f *FakeWorkspace) DeleteUser(_ context.Context, email string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.Users[strings.ToLower(email)]; !ok {
		return ErrWorkspaceNotFound
	}
	delete(f.Users, strings.ToLower(email))
	f.Deleted = append(f.Deleted, email)
	return nil
}

func (f *FakeWorkspace) EnsureGroup(_ context.Context, email, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.Groups[email]; !ok {
		f.Groups[email] = []string{}
	}
	return nil
}

func (f *FakeWorkspace) ListMembers(_ context.Context, group string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, ok := f.Groups[group]
	if !ok {
		return nil, ErrWorkspaceNotFound
	}
	out := append([]string(nil), m...)
	sort.Strings(out)
	return out, nil
}

func (f *FakeWorkspace) AddMember(_ context.Context, group, member string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Groups[group] = append(f.Groups[group], member)
	return nil
}

func (f *FakeWorkspace) RemoveMember(_ context.Context, group, member string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	kept := []string{}
	for _, m := range f.Groups[group] {
		if m != member {
			kept = append(kept, m)
		}
	}
	f.Groups[group] = kept
	return nil
}

// User returns a copy of a fake user (nil if missing).
func (f *FakeWorkspace) User(email string) *WorkspaceUser {
	u, err := f.GetUser(context.Background(), email)
	if err != nil {
		return nil
	}
	return u
}
