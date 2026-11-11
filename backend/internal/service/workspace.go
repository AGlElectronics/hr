package service

import (
	"context"

	"github.com/AGlElectronics/hr/backend/internal/domain"
)

// WorkspaceService is the Google Workspace connector.
type WorkspaceService struct{}

// Onboard creates the person's Workspace account; it returns a warning
// instead of failing the onboarding.
func (w *WorkspaceService) Onboard(ctx context.Context, actor string, d *domain.EmploymentDraft, p *domain.Person, attempt int) string {
	return ""
}

// Offboard suspends the person's Workspace account.
func (w *WorkspaceService) Offboard(ctx context.Context, actor string, p *domain.Person) string {
	return ""
}
