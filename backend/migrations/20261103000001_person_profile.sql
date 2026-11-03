-- Volunteer/person profile: structured name, required private email, contact
-- details, social handles, emergency contact and lifecycle status.
--
-- Existing rows without an email get a reserved `.invalid` placeholder so the
-- column can be NOT NULL; the UI flags these as "email missing" and
-- provisioning refuses them until a real address is set.

-- +goose Up
ALTER TABLE people
    ADD COLUMN first_name        TEXT NOT NULL DEFAULT '',
    ADD COLUMN last_name         TEXT NOT NULL DEFAULT '',
    ADD COLUMN nickname          TEXT,
    ADD COLUMN org_email         TEXT,
    ADD COLUMN phone             TEXT,
    ADD COLUMN address           TEXT,
    ADD COLUMN social_handles    JSONB NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN emergency_contact JSONB NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN status            TEXT NOT NULL DEFAULT 'applicant'
        CHECK (status IN ('applicant', 'onboarding', 'active', 'paused', 'offboarded')),
    ADD COLUMN notes             TEXT;

UPDATE people SET
    first_name = split_part(legal_name, ' ', 1),
    last_name  = CASE WHEN position(' ' IN legal_name) > 0
                      THEN substr(legal_name, position(' ' IN legal_name) + 1)
                      ELSE '' END,
    nickname   = NULLIF(display_name, legal_name);

UPDATE people
   SET email = 'missing-' || left(id::text, 8) || '@email-required.invalid'
 WHERE email IS NULL OR btrim(email) = '';

ALTER TABLE people ALTER COLUMN email SET NOT NULL;

UPDATE people p SET status = 'active'
 WHERE EXISTS (SELECT 1 FROM employment_drafts d WHERE d.person_id = p.id AND d.state = 'active');

UPDATE people p SET status = 'onboarding'
 WHERE status = 'applicant'
   AND EXISTS (SELECT 1 FROM employment_drafts d WHERE d.person_id = p.id
                 AND d.state IN ('draft', 'provisioning', 'pending_approval'));

CREATE INDEX people_status_idx ON people (status);

-- +goose Down
DROP INDEX IF EXISTS people_status_idx;
ALTER TABLE people ALTER COLUMN email DROP NOT NULL;
ALTER TABLE people
    DROP COLUMN notes,
    DROP COLUMN status,
    DROP COLUMN emergency_contact,
    DROP COLUMN social_handles,
    DROP COLUMN address,
    DROP COLUMN phone,
    DROP COLUMN org_email,
    DROP COLUMN nickname,
    DROP COLUMN last_name,
    DROP COLUMN first_name;
