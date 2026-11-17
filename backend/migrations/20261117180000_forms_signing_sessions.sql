-- Forms library with immutable versions, per-onboarding selection, and
-- signing sessions. Library entries (NDA, GDPR, …) live in document_templates;
-- PDF bytes and field mappings live on document_template_versions. HR attaches
-- a pinned version per onboarding. Send emails a token link to the person's
-- private email; Complete waits until required docs are signed or waived.
-- The sign-hr host Sign button is a local stub (no external provider API yet).

-- +goose Up
ALTER TABLE document_templates
    ADD COLUMN IF NOT EXISTS current_version_id UUID;

CREATE TABLE IF NOT EXISTS document_template_versions (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    template_id   UUID NOT NULL REFERENCES document_templates(id) ON DELETE CASCADE,
    version       INTEGER NOT NULL,
    storage_key   TEXT NOT NULL DEFAULT '',
    file_name     TEXT NOT NULL DEFAULT '',
    file_size     INTEGER NOT NULL DEFAULT 0,
    change_note   TEXT NOT NULL DEFAULT '',
    published_by  TEXT NOT NULL DEFAULT '',
    published_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (template_id, version)
);
CREATE INDEX IF NOT EXISTS document_template_versions_template_idx
    ON document_template_versions (template_id);

-- Drop the unfinished fields table from an earlier draft of this migration
-- if it pointed at template_id instead of version_id.
DROP TABLE IF EXISTS document_template_fields;

CREATE TABLE document_template_fields (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    template_version_id UUID NOT NULL REFERENCES document_template_versions(id) ON DELETE CASCADE,
    pdf_field_name      TEXT NOT NULL,
    field_type          TEXT NOT NULL DEFAULT 'text'
                        CHECK (field_type IN ('text', 'choice', 'button')),
    hr_attribute        TEXT NOT NULL DEFAULT '',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (template_version_id, pdf_field_name)
);
CREATE INDEX document_template_fields_version_idx ON document_template_fields (template_version_id);

ALTER TABLE document_templates DROP CONSTRAINT IF EXISTS document_templates_current_version_fk;
ALTER TABLE document_templates
    ADD CONSTRAINT document_templates_current_version_fk
    FOREIGN KEY (current_version_id) REFERENCES document_template_versions(id) ON DELETE SET NULL;

ALTER TABLE onboarding_documents
    ADD COLUMN IF NOT EXISTS template_version_id UUID REFERENCES document_template_versions(id) ON DELETE RESTRICT;

ALTER TABLE onboarding_documents DROP CONSTRAINT IF EXISTS onboarding_documents_status_check;
ALTER TABLE onboarding_documents
    ADD CONSTRAINT onboarding_documents_status_check
    CHECK (status IN ('pending', 'sent', 'signed', 'waived', 'stub_blocked'));

CREATE TABLE IF NOT EXISTS signing_sessions (
    id                     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    onboarding_document_id UUID NOT NULL REFERENCES onboarding_documents(id) ON DELETE CASCADE,
    template_version_id    UUID REFERENCES document_template_versions(id) ON DELETE RESTRICT,
    token                  TEXT NOT NULL UNIQUE,
    status                 TEXT NOT NULL DEFAULT 'sent'
                           CHECK (status IN ('sent', 'signed', 'expired', 'cancelled')),
    signer_email           TEXT NOT NULL DEFAULT '',
    signer_name            TEXT NOT NULL DEFAULT '',
    form_name              TEXT NOT NULL DEFAULT '',
    form_version           INTEGER NOT NULL DEFAULT 0,
    created_by             TEXT NOT NULL DEFAULT '',
    expires_at             TIMESTAMPTZ NOT NULL,
    completed_at           TIMESTAMPTZ,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS signing_sessions_document_idx ON signing_sessions (onboarding_document_id);
CREATE INDEX IF NOT EXISTS signing_sessions_token_idx ON signing_sessions (token);

-- +goose Down
DROP TABLE IF EXISTS signing_sessions;
ALTER TABLE onboarding_documents DROP CONSTRAINT IF EXISTS onboarding_documents_status_check;
ALTER TABLE onboarding_documents
    ADD CONSTRAINT onboarding_documents_status_check
    CHECK (status IN ('pending', 'stub_blocked', 'waived'));
ALTER TABLE onboarding_documents DROP COLUMN IF EXISTS template_version_id;
ALTER TABLE document_templates DROP CONSTRAINT IF EXISTS document_templates_current_version_fk;
ALTER TABLE document_templates DROP COLUMN IF EXISTS current_version_id;
DROP TABLE IF EXISTS document_template_fields;
DROP TABLE IF EXISTS document_template_versions;
