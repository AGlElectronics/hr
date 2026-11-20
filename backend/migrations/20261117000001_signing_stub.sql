-- Signing (stub): document templates HR admins define, and per-onboarding
-- document rows attached to a draft. No signing provider is wired yet, so
-- every document starts in state 'pending', is moved to 'stub_blocked' when
-- something tries to send it for signing, and can be 'waived' by an HR
-- admin for local testing when settings.allow_stub_skip is on. A real
-- provider (DocuSeal / Documenso / QTSP) will replace stub_blocked with
-- sent / signed / declined states later.
--
--   - document_templates: named templates HR admins manage under Settings
--     → Signing. file_name and file_size describe an optional placeholder
--     PDF (the stub never stores file contents); required says whether an
--     onboarding cannot complete without it.
--   - onboarding_documents: one row per (draft, template) attached at
--     onboarding start and when templates are added later. status reflects
--     the stub lifecycle above.

-- +goose Up
CREATE TABLE document_templates (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name          TEXT NOT NULL,
    description   TEXT NOT NULL DEFAULT '',
    required      BOOLEAN NOT NULL DEFAULT true,
    file_name     TEXT NOT NULL DEFAULT '',
    file_size     INTEGER NOT NULL DEFAULT 0,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX document_templates_name_key ON document_templates (lower(name));

CREATE TABLE onboarding_documents (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    draft_id      UUID NOT NULL REFERENCES employment_drafts(id) ON DELETE CASCADE,
    template_id   UUID NOT NULL REFERENCES document_templates(id) ON DELETE RESTRICT,
    required      BOOLEAN NOT NULL DEFAULT true,
    status        TEXT NOT NULL DEFAULT 'pending'
                  CHECK (status IN ('pending', 'stub_blocked', 'waived')),
    status_note   TEXT NOT NULL DEFAULT '',
    waived_by     TEXT NOT NULL DEFAULT '',
    waived_at     TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX onboarding_documents_draft_template_key ON onboarding_documents (draft_id, template_id);
CREATE INDEX onboarding_documents_draft_idx ON onboarding_documents (draft_id);
CREATE INDEX onboarding_documents_template_idx ON onboarding_documents (template_id);

-- +goose Down
DROP TABLE onboarding_documents;
DROP TABLE document_templates;
