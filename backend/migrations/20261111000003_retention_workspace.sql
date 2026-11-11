-- Retention and Google Workspace bookkeeping:
--   - people.retention_warned_at: when HR admins were told this person is
--     about to be purged (cleared whenever the person changes).
--   - workspace_accounts: the Google Workspace account of a person. The row
--     outlives the person (no foreign key): after an offboarding the
--     account is suspended and deleted after the configured time, with a
--     reminder to HR admins first.

-- +goose Up
ALTER TABLE people ADD COLUMN retention_warned_at TIMESTAMPTZ;

CREATE TABLE workspace_accounts (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    person_id     UUID,
    primary_email TEXT NOT NULL UNIQUE,
    display_name  TEXT NOT NULL DEFAULT '',
    status        TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'suspended', 'deleted')),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    suspended_at  TIMESTAMPTZ,
    delete_after  TIMESTAMPTZ,
    reminded_at   TIMESTAMPTZ,
    deleted_at    TIMESTAMPTZ,
    last_error    TEXT NOT NULL DEFAULT ''
);
CREATE INDEX workspace_accounts_person_idx ON workspace_accounts (person_id);
CREATE INDEX workspace_accounts_due_idx ON workspace_accounts (status, delete_after);

-- +goose Down
DROP TABLE workspace_accounts;
ALTER TABLE people DROP COLUMN retention_warned_at;
