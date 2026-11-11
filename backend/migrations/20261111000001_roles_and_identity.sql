-- HR roles, department leads and identity links:
--   - people.idp_user_id is the IdP's own user id (Keycloak user id) and
--     people.idp_subject the OIDC `sub` other modules key on. They are often
--     the same but are stored separately (existing-IdP proposal #15).
--     Backfilled from the latest successful IdP provisioning job.
--   - department_leads is the lead list of each department (a person may
--     lead several). Leads with the hr-lead role act only inside them.
--   - departments.archived_at hides a department from new onboardings;
--     default_bundles are the access groups new members get.
--   - people.paused_at dates a pause (retention counts from it).
--   - audit_events records the actor's issuer and subject, and purge_after
--     marks access history kept after an offboarding until it is purged.

-- +goose Up
ALTER TABLE people
    ADD COLUMN idp_user_id TEXT,
    ADD COLUMN idp_subject TEXT,
    ADD COLUMN paused_at   TIMESTAMPTZ;

UPDATE people p SET idp_user_id = (
    SELECT j.external_id FROM provisioning_jobs j
      JOIN employment_drafts d ON d.id = j.draft_id
     WHERE d.person_id = p.id AND j.target = 'idp' AND j.status = 'succeeded' AND j.external_id IS NOT NULL
     ORDER BY j.started_at DESC LIMIT 1);
UPDATE people SET idp_subject = idp_user_id WHERE idp_user_id IS NOT NULL;
UPDATE people SET paused_at = updated_at WHERE status = 'paused';

CREATE UNIQUE INDEX people_idp_user_id_key ON people (idp_user_id) WHERE idp_user_id IS NOT NULL;
CREATE INDEX people_idp_subject_idx ON people (idp_subject) WHERE idp_subject IS NOT NULL;

CREATE TABLE department_leads (
    department_id UUID NOT NULL REFERENCES departments(id) ON DELETE CASCADE,
    person_id     UUID NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (department_id, person_id)
);
CREATE INDEX department_leads_person_idx ON department_leads (person_id);

ALTER TABLE departments
    ADD COLUMN archived_at     TIMESTAMPTZ,
    ADD COLUMN default_bundles JSONB NOT NULL DEFAULT '[]'::jsonb;

ALTER TABLE audit_events
    ADD COLUMN actor_issuer  TEXT NOT NULL DEFAULT '',
    ADD COLUMN actor_subject TEXT NOT NULL DEFAULT '',
    ADD COLUMN purge_after   TIMESTAMPTZ;
CREATE INDEX audit_events_purge_idx ON audit_events (purge_after) WHERE purge_after IS NOT NULL;
CREATE INDEX audit_events_subject_idx ON audit_events (actor_issuer, actor_subject);

-- +goose Down
DROP INDEX IF EXISTS audit_events_subject_idx;
DROP INDEX IF EXISTS audit_events_purge_idx;
ALTER TABLE audit_events DROP COLUMN purge_after, DROP COLUMN actor_subject, DROP COLUMN actor_issuer;
ALTER TABLE departments DROP COLUMN default_bundles, DROP COLUMN archived_at;
DROP TABLE department_leads;
DROP INDEX IF EXISTS people_idp_subject_idx;
DROP INDEX IF EXISTS people_idp_user_id_key;
ALTER TABLE people DROP COLUMN paused_at, DROP COLUMN idp_subject, DROP COLUMN idp_user_id;
