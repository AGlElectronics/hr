-- +goose Up
-- Pairing with Den (see internal/denlink). One row when paired, none when standalone.
CREATE TABLE den_link (
    id            SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    den_base_url  TEXT NOT NULL,
    credential    TEXT NOT NULL,
    module_id     TEXT NOT NULL DEFAULT '',
    paired_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    synced_at     TIMESTAMPTZ,
    last_error    TEXT NOT NULL DEFAULT '',
    bundle        JSONB
);

-- +goose Down
DROP TABLE den_link;
