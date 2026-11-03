-- Onboarding flow: departments (each backed by an IdP group, with default
-- module access), drafts linked to a department, and the remaining volunteer
-- definition fields on people.

-- +goose Up
ALTER TABLE people
    ADD COLUMN date_of_birth DATE,
    ADD COLUMN is_banned     BOOLEAN NOT NULL DEFAULT false;

CREATE TABLE departments (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    slug           TEXT NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9][a-z0-9-]*$'),
    name           TEXT NOT NULL,
    description    TEXT NOT NULL DEFAULT '',
    group_name     TEXT NOT NULL UNIQUE,
    default_roles  JSONB NOT NULL DEFAULT '[]'::jsonb,
    idp_synced_at  TIMESTAMPTZ,
    idp_sync_error TEXT,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE employment_drafts
    ADD COLUMN department_id UUID REFERENCES departments(id) ON DELETE RESTRICT;

CREATE INDEX employment_drafts_department_idx ON employment_drafts (department_id);

-- +goose Down
DROP INDEX IF EXISTS employment_drafts_department_idx;
ALTER TABLE employment_drafts DROP COLUMN department_id;
DROP TABLE IF EXISTS departments;
ALTER TABLE people DROP COLUMN is_banned, DROP COLUMN date_of_birth;
