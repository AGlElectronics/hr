-- Review queue and spreadsheet imports (existing-IdP proposal #9 - #11):
--   - review_items are draft people waiting for a human: accounts without
--     an hrPersonId found in the IdP (idp_scan), volunteers who filled in
--     the "waiting for a coordinator" form (self_service) and rows of an
--     imported spreadsheet (import). An HR admin, or a lead of the item's
--     department, accepts (creates the person), merges (links to an
--     existing person) or rejects (the account is disabled). Nothing is
--     ever accepted automatically.
--   - import_batches records each import (the uploaded file is not kept,
--     only the mapped fields of rows that failed, for the error report) so
--     it can be undone while none of its items is accepted.
--   - import_templates are saved column mappings ("next year's import").

-- +goose Up
CREATE TABLE import_batches (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    file_name     TEXT NOT NULL DEFAULT '',
    created_by    TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    rows_total    INTEGER NOT NULL DEFAULT 0,
    rows_created  INTEGER NOT NULL DEFAULT 0,
    rows_updated  INTEGER NOT NULL DEFAULT 0,
    rows_failed   INTEGER NOT NULL DEFAULT 0,
    errors        JSONB NOT NULL DEFAULT '[]'::jsonb,
    undone_at     TIMESTAMPTZ
);

CREATE TABLE import_templates (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name       TEXT NOT NULL,
    mapping    JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX import_templates_name_key ON import_templates (lower(name));

CREATE TABLE review_items (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    source          TEXT NOT NULL CHECK (source IN ('idp_scan', 'self_service', 'import')),
    status          TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'ready', 'accepted', 'merged', 'rejected')),
    idp_user_id     TEXT,
    idp_subject     TEXT,
    email           TEXT NOT NULL DEFAULT '',
    email_verified  BOOLEAN NOT NULL DEFAULT false,
    first_name      TEXT NOT NULL DEFAULT '',
    last_name       TEXT NOT NULL DEFAULT '',
    nickname        TEXT NOT NULL DEFAULT '',
    phone           TEXT NOT NULL DEFAULT '',
    data            JSONB NOT NULL DEFAULT '{}'::jsonb,
    department_id   UUID REFERENCES departments(id) ON DELETE SET NULL,
    bundle_ids      JSONB NOT NULL DEFAULT '[]'::jsonb,
    match_person_id UUID REFERENCES people(id) ON DELETE SET NULL,
    match_kind      TEXT NOT NULL DEFAULT '' CHECK (match_kind IN ('', 'verified_email', 'possible')),
    import_batch_id UUID REFERENCES import_batches(id) ON DELETE CASCADE,
    external_ref    TEXT NOT NULL DEFAULT '',
    person_id       UUID REFERENCES people(id) ON DELETE SET NULL,
    draft_id        UUID REFERENCES employment_drafts(id) ON DELETE SET NULL,
    decided_by      TEXT NOT NULL DEFAULT '',
    decided_at      TIMESTAMPTZ,
    decision_note   TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX review_items_idp_user_key ON review_items (idp_user_id) WHERE idp_user_id IS NOT NULL;
CREATE INDEX review_items_subject_idx ON review_items (idp_subject) WHERE idp_subject IS NOT NULL;
CREATE INDEX review_items_status_idx ON review_items (status, department_id);
CREATE INDEX review_items_email_idx ON review_items (lower(email));
CREATE INDEX review_items_batch_idx ON review_items (import_batch_id);

-- +goose Down
DROP TABLE review_items;
DROP TABLE import_templates;
DROP TABLE import_batches;
