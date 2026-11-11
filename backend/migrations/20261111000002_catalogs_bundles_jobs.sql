-- Module role catalogs, access groups (bundles) and background jobs:
--   - catalog_cache keeps the last catalog each module served, so HR can
--     still validate and show roles while a module is offline.
--   - access_bundles are named sets of module roles ("Standard volunteer")
--     that HR admins define; role_plans.bundle_id says which bundle a
--     role came from (NULL: assigned individually), draft_bundles which
--     bundles an onboarding or membership holds.
--   - background_jobs is HR's job queue (provisioning, bundle updates).
--     A job is claimed with a lease; one whose lease ran out is retried
--     until max_attempts.
--   - provisioning_jobs.message never holds a password; any old temporary
--     password message is cleared.

-- +goose Up
CREATE TABLE catalog_cache (
    module     TEXT PRIMARY KEY,
    payload    JSONB NOT NULL,
    source     TEXT NOT NULL DEFAULT '',
    fetched_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE access_bundles (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    roles       JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX access_bundles_name_key ON access_bundles (lower(name));

CREATE TABLE draft_bundles (
    draft_id  UUID NOT NULL REFERENCES employment_drafts(id) ON DELETE CASCADE,
    bundle_id UUID NOT NULL REFERENCES access_bundles(id) ON DELETE RESTRICT,
    PRIMARY KEY (draft_id, bundle_id)
);
CREATE INDEX draft_bundles_bundle_idx ON draft_bundles (bundle_id);

ALTER TABLE role_plans ADD COLUMN bundle_id UUID REFERENCES access_bundles(id) ON DELETE SET NULL;

CREATE TABLE background_jobs (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    kind         TEXT NOT NULL,
    payload      JSONB NOT NULL DEFAULT '{}'::jsonb,
    status       TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'running', 'succeeded', 'failed')),
    attempts     INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 3,
    run_after    TIMESTAMPTZ NOT NULL DEFAULT now(),
    locked_until TIMESTAMPTZ,
    last_error   TEXT NOT NULL DEFAULT '',
    created_by   TEXT NOT NULL DEFAULT '',
    created_by_issuer  TEXT NOT NULL DEFAULT '',
    created_by_subject TEXT NOT NULL DEFAULT '',
    result       JSONB,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at  TIMESTAMPTZ
);
CREATE INDEX background_jobs_due_idx ON background_jobs (status, run_after);

UPDATE provisioning_jobs SET message = 'password_setup_email_failed' WHERE message LIKE 'temporary_password=%';

-- +goose Down
DROP TABLE background_jobs;
ALTER TABLE role_plans DROP COLUMN bundle_id;
DROP TABLE draft_bundles;
DROP TABLE access_bundles;
DROP TABLE catalog_cache;
