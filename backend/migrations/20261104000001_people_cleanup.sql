-- People cleanup:
--   - Offboarding deletes the person, so the "offboarded" status and its
--     bookkeeping columns go away (existing offboarded rows are removed).
--   - The address becomes structured (street, postal code, city, country).
--   - The login name is stored so it can be kept unique and renamed.
--   - The approvals step is gone: onboarding completes straight to active.

-- +goose Up
DELETE FROM employment_drafts d USING people p
 WHERE d.person_id = p.id AND (p.status = 'offboarded' OR p.email LIKE '%@email-required.invalid');
DELETE FROM people WHERE status = 'offboarded' OR email LIKE '%@email-required.invalid';

ALTER TABLE people DROP CONSTRAINT IF EXISTS people_status_check;
ALTER TABLE people ADD CONSTRAINT people_status_check
    CHECK (status IN ('applicant', 'onboarding', 'active', 'paused'));
ALTER TABLE people DROP COLUMN offboarded_at, DROP COLUMN offboard_reason;

ALTER TABLE people DROP COLUMN address;
ALTER TABLE people ADD COLUMN address JSONB NOT NULL DEFAULT '{}'::jsonb;

ALTER TABLE people ADD COLUMN username TEXT;
CREATE UNIQUE INDEX people_username_key ON people (lower(username)) WHERE username IS NOT NULL;
CREATE UNIQUE INDEX people_org_email_key ON people (lower(org_email)) WHERE org_email IS NOT NULL;

UPDATE employment_drafts SET state = 'active' WHERE state = 'pending_approval';
ALTER TABLE employment_drafts DROP CONSTRAINT IF EXISTS employment_drafts_state_check;
ALTER TABLE employment_drafts ADD CONSTRAINT employment_drafts_state_check
    CHECK (state IN ('draft', 'provisioning', 'active', 'rejected'));

DROP TABLE IF EXISTS approvals;

-- +goose Down
CREATE TABLE approvals (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    draft_id      UUID NOT NULL REFERENCES employment_drafts(id) ON DELETE CASCADE,
    decision      TEXT NOT NULL CHECK (decision IN ('approved','rejected')),
    reason        TEXT,
    approved_by   TEXT NOT NULL,
    approved_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
ALTER TABLE employment_drafts DROP CONSTRAINT IF EXISTS employment_drafts_state_check;
ALTER TABLE employment_drafts ADD CONSTRAINT employment_drafts_state_check
    CHECK (state IN ('draft', 'provisioning', 'pending_approval', 'active', 'rejected'));
DROP INDEX IF EXISTS people_org_email_key;
DROP INDEX IF EXISTS people_username_key;
ALTER TABLE people DROP COLUMN username;
ALTER TABLE people DROP COLUMN address;
ALTER TABLE people ADD COLUMN address TEXT;
ALTER TABLE people ADD COLUMN offboarded_at TIMESTAMPTZ, ADD COLUMN offboard_reason TEXT;
ALTER TABLE people DROP CONSTRAINT IF EXISTS people_status_check;
ALTER TABLE people ADD CONSTRAINT people_status_check
    CHECK (status IN ('applicant', 'onboarding', 'active', 'paused', 'offboarded'));
