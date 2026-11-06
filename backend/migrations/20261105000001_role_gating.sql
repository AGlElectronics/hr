-- Roles granted when training is done:
--   - role_requirements: a role ("<module>:<key>") needs a qualification
--     from Learn before its group is given; revoke_on_expiry takes the
--     group away again when the qualification expires.
--   - pending_role_grants: one row per person and gated role. It stays as
--     history while its status moves between pending, granted, overridden
--     and revoked.

-- +goose Up
CREATE TABLE role_requirements (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    role_key          TEXT NOT NULL,
    qualification_key TEXT NOT NULL,
    revoke_on_expiry  BOOLEAN NOT NULL DEFAULT false,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (role_key, qualification_key)
);

CREATE TABLE pending_role_grants (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    person_id   UUID NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    draft_id    UUID REFERENCES employment_drafts(id) ON DELETE SET NULL,
    role_key    TEXT NOT NULL,
    group_name  TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL DEFAULT 'pending'
                CHECK (status IN ('pending', 'granted', 'overridden', 'revoked')),
    reason      TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at TIMESTAMPTZ,
    UNIQUE (person_id, role_key)
);

CREATE INDEX pending_role_grants_status_idx ON pending_role_grants (status);

-- +goose Down
DROP TABLE pending_role_grants;
DROP TABLE role_requirements;
