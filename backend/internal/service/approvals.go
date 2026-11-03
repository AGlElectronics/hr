package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AGlElectronics/hr/backend/internal/audit"
	"github.com/AGlElectronics/hr/backend/internal/db"
	"github.com/AGlElectronics/hr/backend/internal/domain"
	"github.com/AGlElectronics/hr/backend/internal/httperror"
)

// ApprovalService records HR approvals/rejections on drafts.
type ApprovalService struct {
	pool   *pgxpool.Pool
	drafts *DraftService
	audit  *audit.Writer
}

// NewApprovalService wires an ApprovalService.
func NewApprovalService(pool *pgxpool.Pool, drafts *DraftService, aud *audit.Writer) *ApprovalService {
	return &ApprovalService{pool: pool, drafts: drafts, audit: aud}
}

// DecisionInput is the writeable surface of an approval decision.
type DecisionInput struct {
	Decision string `json:"decision"` // "approved" | "rejected"
	Reason   string `json:"reason,omitempty"`
}

// Decide records an approval decision and transitions the draft. Only legal
// from the "pending_approval" state. On "approved" the draft moves to
// "active"; on "rejected" it moves to "rejected".
func (s *ApprovalService) Decide(ctx context.Context, actor string, draftID uuid.UUID, in DecisionInput) (*domain.EmploymentDraft, *domain.Approval, error) {
	in.Decision = strings.ToLower(strings.TrimSpace(in.Decision))
	in.Reason = strings.TrimSpace(in.Reason)

	var next domain.DraftState
	switch domain.ApprovalResult(in.Decision) {
	case domain.ApprovalApproved:
		next = domain.DraftStateActive
	case domain.ApprovalRejected:
		next = domain.DraftStateRejected
	default:
		return nil, nil, httperror.Validation("decision must be 'approved' or 'rejected'", nil)
	}

	var approval *domain.Approval
	err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		var state string
		if err := tx.QueryRow(ctx, `SELECT state FROM employment_drafts WHERE id = $1 FOR UPDATE`, draftID).Scan(&state); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return httperror.NotFound("employment_draft")
			}
			return httperror.Internal(err)
		}
		if domain.DraftState(state) != domain.DraftStatePendingApproval {
			return httperror.Conflict("draft is not awaiting approval")
		}
		id := uuid.New()
		now := time.Now().UTC()
		a := &domain.Approval{}
		if err := tx.QueryRow(ctx, `
INSERT INTO approvals (id, draft_id, decision, reason, approved_by, approved_at)
VALUES ($1, $2, $3, NULLIF($4, ''), $5, $6)
RETURNING id, draft_id, decision, COALESCE(reason, ''), approved_by, approved_at`,
			id, draftID, in.Decision, in.Reason, actor, now).
			Scan(&a.ID, &a.DraftID, &a.Decision, &a.Reason, &a.ApprovedBy, &a.ApprovedAt); err != nil {
			return httperror.Internal(err)
		}
		approval = a
		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	// Transition happens outside the approval insert transaction so a failed
	// transition does not swallow the recorded decision — we want the audit
	// trail even if the state move trips a race.
	updated, err := s.drafts.Transition(ctx, draftID, next)
	if err != nil {
		return nil, approval, err
	}
	if next == domain.DraftStateActive {
		if _, err := s.pool.Exec(ctx, `UPDATE people SET status = 'active', updated_at = now() WHERE id = $1`, updated.PersonID); err != nil {
			return nil, approval, httperror.Internal(err)
		}
	}
	if err := s.audit.Record(ctx, actor, "draft.approval."+in.Decision, "employment_draft", draftID, map[string]any{
		"approval_id": approval.ID,
	}); err != nil {
		return nil, approval, err
	}
	return updated, approval, nil
}

// List returns every approval recorded for a draft.
func (s *ApprovalService) List(ctx context.Context, draftID uuid.UUID) ([]domain.Approval, error) {
	rows, err := s.pool.Query(ctx, `
SELECT id, draft_id, decision, COALESCE(reason, ''), approved_by, approved_at
FROM approvals WHERE draft_id = $1 ORDER BY approved_at DESC`, draftID)
	if err != nil {
		return nil, httperror.Internal(err)
	}
	defer rows.Close()
	out := make([]domain.Approval, 0, 2)
	for rows.Next() {
		var a domain.Approval
		if err := rows.Scan(&a.ID, &a.DraftID, &a.Decision, &a.Reason, &a.ApprovedBy, &a.ApprovedAt); err != nil {
			return nil, httperror.Internal(err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, httperror.Internal(err)
	}
	return out, nil
}
