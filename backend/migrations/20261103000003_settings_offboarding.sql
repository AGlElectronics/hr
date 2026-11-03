-- HR settings (key/value, e.g. the organization email domain) and
-- offboarding bookkeeping on people.

-- +goose Up
CREATE TABLE settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE people
    ADD COLUMN offboarded_at   TIMESTAMPTZ,
    ADD COLUMN offboard_reason TEXT;

-- +goose Down
ALTER TABLE people DROP COLUMN offboard_reason, DROP COLUMN offboarded_at;
DROP TABLE IF EXISTS settings;
