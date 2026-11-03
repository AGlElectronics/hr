package provisioners

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

// WorkspaceAccount is the minimal shape HR hands to the workspace connector
// (planned: Google Workspace).
type WorkspaceAccount struct {
	HRPersonID  uuid.UUID
	Email       string
	DisplayName string
	// Aliases, team mailing lists, quota tier etc. — kept as a flat map
	// so the fake does not model a specific vendor shape.
	Attributes map[string]string
}

// WorkspaceResult is what the connector returns on success.
type WorkspaceResult struct {
	ExternalID string
	MailboxURL string
}

// WorkspaceProvisioner is the connector the HR service calls. Must be
// idempotent on HRPersonID.
type WorkspaceProvisioner interface {
	Provision(ctx context.Context, acc WorkspaceAccount) (WorkspaceResult, error)
}

// FakeWorkspace is the in-memory stub used in local dev and tests.
type FakeWorkspace struct {
	logger zerolog.Logger

	mu    sync.Mutex
	calls []WorkspaceAccount
}

// NewFakeWorkspace returns a FakeWorkspace that logs each call.
func NewFakeWorkspace(logger zerolog.Logger) *FakeWorkspace {
	return &FakeWorkspace{logger: logger.With().Str("component", "fake_workspace").Logger()}
}

// Provision records the call and returns deterministic placeholder values.
func (f *FakeWorkspace) Provision(ctx context.Context, acc WorkspaceAccount) (WorkspaceResult, error) {
	if ctx.Err() != nil {
		return WorkspaceResult{}, ctx.Err()
	}
	if acc.HRPersonID == uuid.Nil {
		return WorkspaceResult{}, fmt.Errorf("fake workspace: HRPersonID required")
	}
	if acc.Email == "" {
		return WorkspaceResult{}, fmt.Errorf("fake workspace: Email required")
	}

	f.mu.Lock()
	f.calls = append(f.calls, acc)
	f.mu.Unlock()

	ext := "ws:" + acc.HRPersonID.String()
	url := "https://workspace.local/mailbox/" + acc.HRPersonID.String()
	f.logger.Info().
		Str("hr_person_id", acc.HRPersonID.String()).
		Str("email", acc.Email).
		Str("external_id", ext).
		Time("at", time.Now().UTC()).
		Msg("fake workspace provision")

	return WorkspaceResult{ExternalID: ext, MailboxURL: url}, nil
}

// Calls returns a snapshot of every Provision call made so far.
func (f *FakeWorkspace) Calls() []WorkspaceAccount {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]WorkspaceAccount, len(f.calls))
	copy(out, f.calls)
	return out
}
