package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/AGlElectronics/hr/backend/internal/audit"
	"github.com/AGlElectronics/hr/backend/internal/domain"
)

// NotifyService is the stub user-notification channel. Real backends
// (email, Keycloak action tokens, Matrix) land in future phases behind the
// same Notify method so handlers don't need to change.
type NotifyService struct {
	logger zerolog.Logger
	drafts *DraftService
	people *PeopleService
	audit  *audit.Writer
}

// NewNotifyService wires a NotifyService.
func NewNotifyService(logger zerolog.Logger, drafts *DraftService, people *PeopleService, aud *audit.Writer) *NotifyService {
	return &NotifyService{
		logger: logger.With().Str("component", "notify").Logger(),
		drafts: drafts,
		people: people,
		audit:  aud,
	}
}

// NotifyInput controls what the user receives. In this scaffold both fields
// are advisory and only end up in the log line and audit metadata.
type NotifyInput struct {
	Subject string `json:"subject,omitempty"`
	Message string `json:"message,omitempty"`
}

// Receipt is returned to callers so API clients can prove the notify step
// happened. External id is empty on the stub because nothing was actually
// sent anywhere.
type Receipt struct {
	Channel    string    `json:"channel"`
	Recipient  string    `json:"recipient"`
	Subject    string    `json:"subject"`
	ExternalID string    `json:"external_id,omitempty"`
	DraftID    uuid.UUID `json:"draft_id"`
	PersonID   uuid.UUID `json:"person_id"`
}

// Notify emits a log line and audit event representing "we told the user
// their account is live". Returns ErrNotFound if the draft or person is
// missing, Conflict if the draft is not in an active-ish state.
func (s *NotifyService) Notify(ctx context.Context, actor string, draftID uuid.UUID, in NotifyInput) (*Receipt, error) {
	draft, err := s.drafts.Get(ctx, draftID)
	if err != nil {
		return nil, err
	}
	// We only notify once a draft is live, matching the product flow
	// (HR approves -> user notified). Rejected drafts never notify.
	if draft.State != domain.DraftStateActive {
		return nil, fmt.Errorf("notify is only allowed when draft is active (state=%s)", draft.State)
	}
	person, err := s.people.Get(ctx, draft.PersonID)
	if err != nil {
		return nil, err
	}

	subject := strings.TrimSpace(in.Subject)
	if subject == "" {
		subject = "Your account is ready"
	}
	msg := strings.TrimSpace(in.Message)
	if msg == "" {
		msg = "Your HR onboarding is complete. Sign in via the identity provider to continue."
	}

	recipient := person.Email
	if recipient == "" {
		recipient = person.DisplayName
	}

	s.logger.Info().
		Str("actor", actor).
		Str("draft_id", draft.ID.String()).
		Str("person_id", person.ID.String()).
		Str("recipient", recipient).
		Str("subject", subject).
		Str("message", msg).
		Msg("notify stub")

	if err := s.audit.Record(ctx, actor, "draft.notify", "employment_draft", draft.ID, map[string]any{
		"recipient": recipient,
		"subject":   subject,
	}); err != nil {
		return nil, err
	}

	return &Receipt{
		Channel:   "log",
		Recipient: recipient,
		Subject:   subject,
		DraftID:   draft.ID,
		PersonID:  person.ID,
	}, nil
}
