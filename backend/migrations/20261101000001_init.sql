-- Initial schema for the HR provisioning module.
--
-- Design notes:
--   - `people` is the stable internal identity. It is NOT a login record;
--     authentication is handled by the IdP (Keycloak, planned). See ADR-0001.
--   - `employment_drafts` carry the draft -> provisioning -> pending_approval
--     -> active|rejected state machine (see internal/domain/domain.go).
--   - `role_plans` are the desired role/group assignments for a draft.
--     Frozen once provisioning begins so audit matches what was pushed.
--   - `provisioning_jobs` are append-only attempts at pushing a draft to
--     an external system (IdP, workspace). Retries create new rows.
--   - `approvals` are HR's final decision on a draft.
--   - `audit_events` captures every state-changing write.

-- +goose Up
-- +goose StatementBegin
CREATE EXTENSION IF NOT EXISTS pgcrypto;
-- +goose StatementEnd

-- ----------------------------------------------------------------------------
-- PEOPLE
-- ----------------------------------------------------------------------------

CREATE TABLE people (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    legal_name    TEXT NOT NULL,
    display_name  TEXT NOT NULL,
    email         TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX people_email_idx         ON people (lower(email));
CREATE INDEX people_display_name_idx  ON people (lower(display_name));

-- ----------------------------------------------------------------------------
-- EMPLOYMENT DRAFTS
-- ----------------------------------------------------------------------------

CREATE TABLE employment_drafts (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    person_id   UUID NOT NULL REFERENCES people(id) ON DELETE RESTRICT,
    title       TEXT NOT NULL,
    start_date  TIMESTAMPTZ,
    end_date    TIMESTAMPTZ,
    state       TEXT NOT NULL DEFAULT 'draft' CHECK (state IN (
        'draft','provisioning','pending_approval','active','rejected'
    )),
    notes       TEXT NOT NULL DEFAULT '',
    created_by  TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX employment_drafts_person_idx  ON employment_drafts (person_id);
CREATE INDEX employment_drafts_state_idx   ON employment_drafts (state);
CREATE INDEX employment_drafts_updated_idx ON employment_drafts (updated_at DESC);

-- ----------------------------------------------------------------------------
-- ROLE PLANS
-- ----------------------------------------------------------------------------

CREATE TABLE role_plans (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    draft_id    UUID NOT NULL REFERENCES employment_drafts(id) ON DELETE CASCADE,
    role_name   TEXT NOT NULL,
    group_name  TEXT,
    scope       TEXT NOT NULL DEFAULT 'global',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (draft_id, role_name, scope)
);

CREATE INDEX role_plans_draft_idx ON role_plans (draft_id);

-- ----------------------------------------------------------------------------
-- PROVISIONING JOBS (append-only; retries create new rows)
-- ----------------------------------------------------------------------------

CREATE TABLE provisioning_jobs (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    draft_id      UUID NOT NULL REFERENCES employment_drafts(id) ON DELETE CASCADE,
    target        TEXT NOT NULL CHECK (target IN ('idp','workspace')),
    status        TEXT NOT NULL CHECK (status IN ('pending','running','succeeded','failed')),
    attempt       INTEGER NOT NULL DEFAULT 1 CHECK (attempt >= 1),
    message       TEXT,
    external_id   TEXT,
    started_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at   TIMESTAMPTZ
);

CREATE INDEX provisioning_jobs_draft_idx  ON provisioning_jobs (draft_id, started_at DESC);
CREATE INDEX provisioning_jobs_target_idx ON provisioning_jobs (target);
CREATE INDEX provisioning_jobs_status_idx ON provisioning_jobs (status);

-- ----------------------------------------------------------------------------
-- APPROVALS
-- ----------------------------------------------------------------------------

CREATE TABLE approvals (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    draft_id      UUID NOT NULL REFERENCES employment_drafts(id) ON DELETE CASCADE,
    decision      TEXT NOT NULL CHECK (decision IN ('approved','rejected')),
    reason        TEXT,
    approved_by   TEXT NOT NULL,
    approved_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX approvals_draft_idx ON approvals (draft_id, approved_at DESC);

-- ----------------------------------------------------------------------------
-- AUDIT EVENTS (append-only; one row per state-changing write)
-- ----------------------------------------------------------------------------

CREATE TABLE audit_events (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_name    TEXT NOT NULL,
    action        TEXT NOT NULL,
    entity_type   TEXT NOT NULL,
    entity_id     UUID NOT NULL,
    metadata      JSONB,
    occurred_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX audit_events_entity_idx    ON audit_events (entity_type, entity_id);
CREATE INDEX audit_events_actor_idx     ON audit_events (actor_name);
CREATE INDEX audit_events_occurred_idx  ON audit_events (occurred_at DESC);

-- +goose Down
DROP TABLE IF EXISTS audit_events;
DROP TABLE IF EXISTS approvals;
DROP TABLE IF EXISTS provisioning_jobs;
DROP TABLE IF EXISTS role_plans;
DROP TABLE IF EXISTS employment_drafts;
DROP TABLE IF EXISTS people;
