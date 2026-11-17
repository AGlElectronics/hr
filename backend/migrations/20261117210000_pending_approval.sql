-- +goose Up
-- Restore pending_approval: onboarding packet is locked, forms are out for
-- signature, and HR Approves only after a human has checked the signed forms.
-- Provisioning still does not run until Approve.
UPDATE employment_drafts SET state = 'draft' WHERE state = 'pending_approval';
ALTER TABLE employment_drafts DROP CONSTRAINT IF EXISTS employment_drafts_state_check;
ALTER TABLE employment_drafts ADD CONSTRAINT employment_drafts_state_check
    CHECK (state IN ('draft', 'pending_approval', 'provisioning', 'active', 'rejected'));

-- +goose Down
UPDATE employment_drafts SET state = 'draft' WHERE state = 'pending_approval';
ALTER TABLE employment_drafts DROP CONSTRAINT IF EXISTS employment_drafts_state_check;
ALTER TABLE employment_drafts ADD CONSTRAINT employment_drafts_state_check
    CHECK (state IN ('draft', 'provisioning', 'active', 'rejected'));
