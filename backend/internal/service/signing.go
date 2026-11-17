package service

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AGlElectronics/hr/backend/internal/audit"
	"github.com/AGlElectronics/hr/backend/internal/domain"
	"github.com/AGlElectronics/hr/backend/internal/files"
	"github.com/AGlElectronics/hr/backend/internal/httperror"
	"github.com/AGlElectronics/hr/backend/internal/mail"
	"github.com/AGlElectronics/hr/backend/internal/pdfform"
)

// SigningStubCode is kept for the Sign button stub on sign-hr (no external provider).
const SigningStubCode httperror.Code = "signing_provider_not_connected"

// SigningStubMessage explains the Sign button is local-only.
const SigningStubMessage = "Signing provider not connected. The Sign button on sign-hr marks the document signed locally; connect a provider later to replace that stub."

const (
	DocPending     = "pending"
	DocSent        = "sent"
	DocSigned      = "signed"
	DocWaived      = "waived"
	DocStubBlocked = "stub_blocked"
)

const signingSessionTTL = 30 * 24 * time.Hour

// DocumentTemplate is a library form (NDA, GDPR, …). PDF and mappings live on versions.
type DocumentTemplate struct {
	ID               uuid.UUID                `json:"id"`
	Name             string                   `json:"name"`
	Description      string                   `json:"description"`
	Required         bool                     `json:"required"`
	CurrentVersionID *uuid.UUID               `json:"current_version_id,omitempty"`
	CurrentVersion   *DocumentTemplateVersion `json:"current_version,omitempty"`
	CreatedAt        time.Time                `json:"created_at"`
	UpdatedAt        time.Time                `json:"updated_at"`
}

// DocumentTemplateInput is the writeable surface of a library form (no PDF).
type DocumentTemplateInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
}

// DocumentTemplateVersion is an immutable published snapshot of a form.
type DocumentTemplateVersion struct {
	ID          uuid.UUID               `json:"id"`
	TemplateID  uuid.UUID               `json:"template_id"`
	Version     int                     `json:"version"`
	FileName    string                  `json:"file_name,omitempty"`
	FileSize    int64                   `json:"file_size,omitempty"`
	HasPDF      bool                    `json:"has_pdf"`
	ChangeNote  string                  `json:"change_note,omitempty"`
	PublishedBy string                  `json:"published_by,omitempty"`
	PublishedAt time.Time               `json:"published_at"`
	Fields      []DocumentTemplateField `json:"fields,omitempty"`
}

// DocumentTemplateField maps one PDF AcroForm field to a person attribute.
type DocumentTemplateField struct {
	ID           uuid.UUID `json:"id"`
	PDFFieldName string    `json:"pdf_field_name"`
	FieldType    string    `json:"field_type"`
	HRAttribute  string    `json:"hr_attribute"`
}

// FieldMappingInput is one mapping when publishing a version.
type FieldMappingInput struct {
	PDFFieldName string `json:"pdf_field_name"`
	FieldType    string `json:"field_type"`
	HRAttribute  string `json:"hr_attribute"`
}

// OnboardingDocument is one form attached to an onboarding, pinned to a version.
type OnboardingDocument struct {
	ID              uuid.UUID  `json:"id"`
	DraftID         uuid.UUID  `json:"draft_id"`
	TemplateID      uuid.UUID  `json:"template_id"`
	TemplateName    string     `json:"template_name"`
	TemplateVersion int        `json:"template_version,omitempty"`
	VersionID       *uuid.UUID `json:"template_version_id,omitempty"`
	Required        bool       `json:"required"`
	Status          string     `json:"status"`
	StatusNote      string     `json:"status_note,omitempty"`
	WaivedBy        string     `json:"waived_by,omitempty"`
	WaivedAt        *time.Time `json:"waived_at,omitempty"`
	SignURL         string     `json:"sign_url,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// SendResult is returned after emailing a sign link.
type SendResult struct {
	Document *OnboardingDocument `json:"document"`
	SignURL  string              `json:"sign_url"`
}

// PublicSigningSession is what sign-hr shows (no staff auth).
type PublicSigningSession struct {
	FormName    string    `json:"form_name"`
	FormVersion int       `json:"form_version"`
	SignerName  string    `json:"signer_name"`
	Status      string    `json:"status"`
	HasPDF      bool      `json:"has_pdf"`
	ExpiresAt   time.Time `json:"expires_at"`
	Message     string    `json:"message,omitempty"`
}

// SigningService owns the forms library, onboarding documents, and sessions.
type SigningService struct {
	pool      *pgxpool.Pool
	audit     *audit.Writer
	settings  *SettingsService
	files     *files.Client // nil: PDF upload/fill unavailable
	mailer    mail.Sender   // nil: emails disabled
	notify    *AdminNotifier
	publicURL string
	appBase   string
}

// NewSigningService wires a SigningService. files/mailer/notify may be nil.
func NewSigningService(pool *pgxpool.Pool, aud *audit.Writer, settings *SettingsService) *SigningService {
	return &SigningService{pool: pool, audit: aud, settings: settings}
}

// SetStorage wires S3 for form PDFs.
func (s *SigningService) SetStorage(c *files.Client) { s.files = c }

// SetMailer wires SMTP for volunteer sign links.
func (s *SigningService) SetMailer(m mail.Sender) { s.mailer = m }

// SetNotifier wires admin emails when all required forms are signed.
func (s *SigningService) SetNotifier(n *AdminNotifier) { s.notify = n }

// SetPublicURL sets SIGNING_PUBLIC_URL (sign-hr host, no trailing slash).
func (s *SigningService) SetPublicURL(u, appBase string) {
	s.publicURL = strings.TrimRight(strings.TrimSpace(u), "/")
	s.appBase = strings.TrimRight(strings.TrimSpace(appBase), "/")
}

// ----- templates -----

// ListTemplates returns every library form by name.
func (s *SigningService) ListTemplates(ctx context.Context) ([]DocumentTemplate, error) {
	rows, err := s.pool.Query(ctx, `
SELECT id, name, description, required, current_version_id, created_at, updated_at
  FROM document_templates ORDER BY lower(name)`)
	if err != nil {
		return nil, httperror.Internal(err)
	}
	defer rows.Close()
	out := []DocumentTemplate{}
	for rows.Next() {
		var t DocumentTemplate
		if err := rows.Scan(&t.ID, &t.Name, &t.Description, &t.Required, &t.CurrentVersionID, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, httperror.Internal(err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, httperror.Internal(err)
	}
	for i := range out {
		if out[i].CurrentVersionID != nil {
			v, err := s.getVersion(ctx, *out[i].CurrentVersionID, false)
			if err == nil {
				out[i].CurrentVersion = v
			}
		}
	}
	return out, nil
}

// GetTemplate returns one library form with its current version and fields.
func (s *SigningService) GetTemplate(ctx context.Context, id uuid.UUID) (*DocumentTemplate, error) {
	t := &DocumentTemplate{}
	err := s.pool.QueryRow(ctx, `
SELECT id, name, description, required, current_version_id, created_at, updated_at
  FROM document_templates WHERE id = $1`, id).
		Scan(&t.ID, &t.Name, &t.Description, &t.Required, &t.CurrentVersionID, &t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, httperror.NotFound("document template")
	}
	if err != nil {
		return nil, httperror.Internal(err)
	}
	if t.CurrentVersionID != nil {
		v, err := s.getVersion(ctx, *t.CurrentVersionID, true)
		if err != nil {
			return nil, err
		}
		t.CurrentVersion = v
	}
	return t, nil
}

func (s *SigningService) normalizeTemplate(in *DocumentTemplateInput) error {
	in.Name = strings.TrimSpace(in.Name)
	in.Description = strings.TrimSpace(in.Description)
	if in.Name == "" {
		return httperror.Validation("validation failed", map[string]string{"name": "required"})
	}
	if len(in.Name) > 120 {
		return httperror.Validation("validation failed", map[string]string{"name": "at most 120 characters"})
	}
	if len(in.Description) > 500 {
		return httperror.Validation("validation failed", map[string]string{"description": "at most 500 characters"})
	}
	return nil
}

// CreateTemplate adds a library form without a PDF yet.
func (s *SigningService) CreateTemplate(ctx context.Context, actor string, in DocumentTemplateInput) (*DocumentTemplate, error) {
	if err := s.normalizeTemplate(&in); err != nil {
		return nil, err
	}
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `
INSERT INTO document_templates (name, description, required)
VALUES ($1, $2, $3) RETURNING id`, in.Name, in.Description, in.Required).Scan(&id)
	if pgErr := new(pgconn.PgError); errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return nil, httperror.Validation("validation failed", map[string]string{"name": "another document template has this name"})
	}
	if err != nil {
		return nil, httperror.Internal(err)
	}
	if err := s.audit.Record(ctx, actor, "signing.template.create", "document_template", id, map[string]any{
		"name": in.Name, "required": in.Required,
	}); err != nil {
		return nil, err
	}
	return s.GetTemplate(ctx, id)
}

// UpdateTemplate changes name/description/required default (not PDF/mappings).
func (s *SigningService) UpdateTemplate(ctx context.Context, actor string, id uuid.UUID, in DocumentTemplateInput) (*DocumentTemplate, error) {
	if _, err := s.GetTemplate(ctx, id); err != nil {
		return nil, err
	}
	if err := s.normalizeTemplate(&in); err != nil {
		return nil, err
	}
	_, err := s.pool.Exec(ctx, `
UPDATE document_templates
   SET name = $2, description = $3, required = $4, updated_at = now()
 WHERE id = $1`, id, in.Name, in.Description, in.Required)
	if pgErr := new(pgconn.PgError); errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return nil, httperror.Validation("validation failed", map[string]string{"name": "another document template has this name"})
	}
	if err != nil {
		return nil, httperror.Internal(err)
	}
	if err := s.audit.Record(ctx, actor, "signing.template.update", "document_template", id, map[string]any{
		"name": in.Name, "required": in.Required,
	}); err != nil {
		return nil, err
	}
	return s.GetTemplate(ctx, id)
}

// DeleteTemplate removes a library form not attached to any onboarding.
func (s *SigningService) DeleteTemplate(ctx context.Context, actor string, id uuid.UUID) error {
	t, err := s.GetTemplate(ctx, id)
	if err != nil {
		return err
	}
	var inUse int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM onboarding_documents WHERE template_id = $1`, id).Scan(&inUse); err != nil {
		return httperror.Internal(err)
	}
	if inUse > 0 {
		return httperror.Conflict("this document template is still attached to one or more onboardings; remove it from them first")
	}
	if _, err := s.pool.Exec(ctx, `DELETE FROM document_templates WHERE id = $1`, id); err != nil {
		return httperror.Internal(err)
	}
	return s.audit.Record(ctx, actor, "signing.template.delete", "document_template", id, map[string]any{"name": t.Name})
}

// ListVersions returns every published version of a template (newest first).
func (s *SigningService) ListVersions(ctx context.Context, templateID uuid.UUID) ([]DocumentTemplateVersion, error) {
	if _, err := s.GetTemplate(ctx, templateID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
SELECT id, template_id, version, file_name, file_size, storage_key, change_note, published_by, published_at
  FROM document_template_versions WHERE template_id = $1 ORDER BY version DESC`, templateID)
	if err != nil {
		return nil, httperror.Internal(err)
	}
	defer rows.Close()
	out := []DocumentTemplateVersion{}
	for rows.Next() {
		var v DocumentTemplateVersion
		var key string
		if err := rows.Scan(&v.ID, &v.TemplateID, &v.Version, &v.FileName, &v.FileSize, &key, &v.ChangeNote, &v.PublishedBy, &v.PublishedAt); err != nil {
			return nil, httperror.Internal(err)
		}
		v.HasPDF = key != ""
		out = append(out, v)
	}
	return out, rows.Err()
}

// DiscoveredPDFField is one AcroForm field found in an uploaded PDF.
type DiscoveredPDFField struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// InspectPDF lists AcroForm field names/types from PDF bytes (no storage).
func (s *SigningService) InspectPDF(pdfBytes []byte) ([]DiscoveredPDFField, error) {
	if len(pdfBytes) == 0 {
		return nil, httperror.Validation("validation failed", map[string]string{"file": "PDF required"})
	}
	discovered, err := pdfform.ListFields(pdfBytes)
	if err != nil {
		return nil, httperror.Validation("validation failed", map[string]string{"file": "not a readable AcroForm PDF: " + err.Error()})
	}
	out := make([]DiscoveredPDFField, 0, len(discovered))
	for _, d := range discovered {
		out = append(out, DiscoveredPDFField{Name: d.Name, Type: d.Type})
	}
	return out, nil
}

// PublishVersion stores a new immutable PDF + field mappings and makes it current.
func (s *SigningService) PublishVersion(ctx context.Context, actor string, templateID uuid.UUID, pdfName string, pdfBytes []byte, fields []FieldMappingInput, changeNote string) (*DocumentTemplateVersion, error) {
	t, err := s.GetTemplate(ctx, templateID)
	if err != nil {
		return nil, err
	}
	if len(pdfBytes) == 0 {
		return nil, httperror.Validation("validation failed", map[string]string{"file": "PDF required"})
	}
	if s.files == nil {
		return nil, httperror.Conflict("object storage is not configured; set S3_* to upload form PDFs")
	}
	pdfName = strings.TrimSpace(pdfName)
	if pdfName == "" {
		pdfName = "form.pdf"
	}
	changeNote = strings.TrimSpace(changeNote)
	if len(changeNote) > 500 {
		return nil, httperror.Validation("validation failed", map[string]string{"change_note": "at most 500 characters"})
	}

	discovered, err := pdfform.ListFields(pdfBytes)
	if err != nil {
		return nil, httperror.Validation("validation failed", map[string]string{"file": "not a readable AcroForm PDF: " + err.Error()})
	}
	if len(fields) == 0 {
		for _, d := range discovered {
			fields = append(fields, FieldMappingInput{PDFFieldName: d.Name, FieldType: d.Type})
		}
	}
	normFields, err := normalizeFieldMappings(fields)
	if err != nil {
		return nil, err
	}

	var next int
	if err := s.pool.QueryRow(ctx, `
SELECT COALESCE(MAX(version), 0) + 1 FROM document_template_versions WHERE template_id = $1`, templateID).Scan(&next); err != nil {
		return nil, httperror.Internal(err)
	}
	vid := uuid.New()
	key := fmt.Sprintf("forms/%s/v%d/%s.pdf", templateID.String(), next, vid.String())
	if err := s.files.PutObject(ctx, key, bytes.NewReader(pdfBytes), int64(len(pdfBytes)), "application/pdf"); err != nil {
		return nil, httperror.Internal(err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, httperror.Internal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, `
INSERT INTO document_template_versions
  (id, template_id, version, storage_key, file_name, file_size, change_note, published_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		vid, templateID, next, key, pdfName, len(pdfBytes), changeNote, actor)
	if err != nil {
		return nil, httperror.Internal(err)
	}
	for _, f := range normFields {
		if _, err := tx.Exec(ctx, `
INSERT INTO document_template_fields (template_version_id, pdf_field_name, field_type, hr_attribute)
VALUES ($1, $2, $3, $4)`, vid, f.PDFFieldName, f.FieldType, f.HRAttribute); err != nil {
			return nil, httperror.Internal(err)
		}
	}
	if _, err := tx.Exec(ctx, `
UPDATE document_templates SET current_version_id = $2, file_name = $3, file_size = $4, updated_at = now()
 WHERE id = $1`, templateID, vid, pdfName, len(pdfBytes)); err != nil {
		return nil, httperror.Internal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, httperror.Internal(err)
	}
	_ = t
	if err := s.audit.Record(ctx, actor, "signing.template.publish", "document_template", templateID, map[string]any{
		"version": next, "fields": len(normFields), "change_note": changeNote,
	}); err != nil {
		return nil, err
	}
	return s.getVersion(ctx, vid, true)
}

func normalizeFieldMappings(in []FieldMappingInput) ([]FieldMappingInput, error) {
	out := make([]FieldMappingInput, 0, len(in))
	seen := map[string]bool{}
	for _, f := range in {
		name := strings.TrimSpace(f.PDFFieldName)
		if name == "" {
			continue
		}
		if seen[name] {
			return nil, httperror.Validation("validation failed", map[string]string{"fields": "duplicate pdf_field_name " + name})
		}
		seen[name] = true
		ft := strings.TrimSpace(f.FieldType)
		if ft == "" {
			ft = "text"
		}
		if ft != "text" && ft != "choice" && ft != "button" {
			return nil, httperror.Validation("validation failed", map[string]string{"fields": "field_type must be text, choice, or button"})
		}
		out = append(out, FieldMappingInput{
			PDFFieldName: name,
			FieldType:    ft,
			HRAttribute:  strings.TrimSpace(f.HRAttribute),
		})
	}
	return out, nil
}

func (s *SigningService) getVersion(ctx context.Context, id uuid.UUID, withFields bool) (*DocumentTemplateVersion, error) {
	v := &DocumentTemplateVersion{}
	var key string
	err := s.pool.QueryRow(ctx, `
SELECT id, template_id, version, file_name, file_size, storage_key, change_note, published_by, published_at
  FROM document_template_versions WHERE id = $1`, id).
		Scan(&v.ID, &v.TemplateID, &v.Version, &v.FileName, &v.FileSize, &key, &v.ChangeNote, &v.PublishedBy, &v.PublishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, httperror.NotFound("document template version")
	}
	if err != nil {
		return nil, httperror.Internal(err)
	}
	v.HasPDF = key != ""
	if !withFields {
		return v, nil
	}
	rows, err := s.pool.Query(ctx, `
SELECT id, pdf_field_name, field_type, hr_attribute
  FROM document_template_fields WHERE template_version_id = $1 ORDER BY pdf_field_name`, id)
	if err != nil {
		return nil, httperror.Internal(err)
	}
	defer rows.Close()
	v.Fields = []DocumentTemplateField{}
	for rows.Next() {
		var f DocumentTemplateField
		if err := rows.Scan(&f.ID, &f.PDFFieldName, &f.FieldType, &f.HRAttribute); err != nil {
			return nil, httperror.Internal(err)
		}
		v.Fields = append(v.Fields, f)
	}
	return v, rows.Err()
}

// ----- onboarding documents -----

// AttachDefaults is a no-op: HR picks forms per onboarding.
func (s *SigningService) AttachDefaults(ctx context.Context, draftID uuid.UUID) error {
	_ = ctx
	_ = draftID
	return nil
}

// AttachTemplate pins the template's current published version onto the draft.
func (s *SigningService) AttachTemplate(ctx context.Context, actor string, draftID, templateID uuid.UUID) (*OnboardingDocument, error) {
	t, err := s.GetTemplate(ctx, templateID)
	if err != nil {
		return nil, err
	}
	if t.CurrentVersionID == nil {
		return nil, httperror.Conflict("publish a form version (PDF + field mapping) before attaching this form")
	}
	var id uuid.UUID
	// Attached forms always block Complete until signed/waived (BlockingDocs).
	// The library "required" flag is a default hint for operators, not a skip.
	err = s.pool.QueryRow(ctx, `
INSERT INTO onboarding_documents (draft_id, template_id, template_version_id, required, status)
VALUES ($1, $2, $3, true, 'pending')
ON CONFLICT (draft_id, template_id) DO NOTHING
RETURNING id`, draftID, templateID, *t.CurrentVersionID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, httperror.Conflict("this form is already attached to the onboarding")
	}
	if err != nil {
		return nil, httperror.Internal(err)
	}
	if err := s.audit.Record(ctx, actor, "signing.document.attach", "onboarding_document", id, map[string]any{
		"draft_id": draftID, "template_id": templateID, "version_id": *t.CurrentVersionID,
	}); err != nil {
		return nil, err
	}
	return s.getDocument(ctx, draftID, id)
}

// DetachDocument removes a pending (not yet sent) form from the onboarding.
func (s *SigningService) DetachDocument(ctx context.Context, actor string, draftID, documentID uuid.UUID) error {
	d, err := s.getDocument(ctx, draftID, documentID)
	if err != nil {
		return err
	}
	if d.Status != DocPending {
		return httperror.Conflict("only pending forms can be detached; cancel or waive a sent form instead")
	}
	if _, err := s.pool.Exec(ctx, `DELETE FROM onboarding_documents WHERE id = $1 AND draft_id = $2`, documentID, draftID); err != nil {
		return httperror.Internal(err)
	}
	return s.audit.Record(ctx, actor, "signing.document.detach", "onboarding_document", documentID, map[string]any{
		"draft_id": draftID, "template_name": d.TemplateName,
	})
}

// ListForDraft returns forms attached to an onboarding (no auto-attach).
func (s *SigningService) ListForDraft(ctx context.Context, draftID uuid.UUID) ([]OnboardingDocument, error) {
	rows, err := s.pool.Query(ctx, `
SELECT d.id, d.draft_id, d.template_id, t.name, COALESCE(v.version, 0), d.template_version_id,
       d.required, d.status, COALESCE(d.status_note, ''),
       COALESCE(d.waived_by, ''), d.waived_at, d.created_at, d.updated_at
  FROM onboarding_documents d
  JOIN document_templates t ON t.id = d.template_id
  LEFT JOIN document_template_versions v ON v.id = d.template_version_id
 WHERE d.draft_id = $1
 ORDER BY lower(t.name)`, draftID)
	if err != nil {
		return nil, httperror.Internal(err)
	}
	defer rows.Close()
	out := []OnboardingDocument{}
	for rows.Next() {
		var d OnboardingDocument
		if err := rows.Scan(&d.ID, &d.DraftID, &d.TemplateID, &d.TemplateName, &d.TemplateVersion, &d.VersionID,
			&d.Required, &d.Status, &d.StatusNote, &d.WaivedBy, &d.WaivedAt, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, httperror.Internal(err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// SendForSigning emails a sign-hr link to the volunteer's private email.
func (s *SigningService) SendForSigning(ctx context.Context, actor string, draftID, documentID uuid.UUID) (*SendResult, error) {
	if s.publicURL == "" {
		return nil, httperror.Conflict("SIGNING_PUBLIC_URL is not set")
	}
	doc, err := s.getDocument(ctx, draftID, documentID)
	if err != nil {
		return nil, err
	}
	if doc.Status == DocSigned || doc.Status == DocWaived {
		return nil, httperror.Conflict("this form is already " + doc.Status)
	}
	if doc.VersionID == nil {
		return nil, httperror.Conflict("this form has no pinned version; detach and attach it again after publishing a version")
	}
	person, err := s.personForDraft(ctx, draftID)
	if err != nil {
		return nil, err
	}
	email := strings.TrimSpace(person.Email)
	if email == "" {
		return nil, httperror.Validation("validation failed", map[string]string{"email": "the volunteer needs a private email before forms can be sent"})
	}
	if s.mailer == nil {
		return nil, httperror.Conflict("email is not configured; set SMTP_* or Den SMTP to send sign links")
	}

	token, err := randomToken(32)
	if err != nil {
		return nil, httperror.Internal(err)
	}
	expires := time.Now().UTC().Add(signingSessionTTL)
	signerName := person.DisplayName
	if signerName == "" {
		signerName = strings.TrimSpace(person.FirstName + " " + person.LastName)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, httperror.Internal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Cancel earlier open sessions for this document.
	_, _ = tx.Exec(ctx, `
UPDATE signing_sessions SET status = 'cancelled', updated_at = now()
 WHERE onboarding_document_id = $1 AND status = 'sent'`, documentID)

	var sessionID uuid.UUID
	if err := tx.QueryRow(ctx, `
INSERT INTO signing_sessions
  (onboarding_document_id, template_version_id, token, status, signer_email, signer_name, form_name, form_version, created_by, expires_at)
VALUES ($1, $2, $3, 'sent', $4, $5, $6, $7, $8, $9) RETURNING id`,
		documentID, *doc.VersionID, token, email, signerName, doc.TemplateName, doc.TemplateVersion, actor, expires).Scan(&sessionID); err != nil {
		return nil, httperror.Internal(err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE onboarding_documents
   SET status = 'sent', status_note = $3, updated_at = now()
 WHERE id = $1 AND draft_id = $2`, documentID, draftID, "Sign link emailed to "+email); err != nil {
		return nil, httperror.Internal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, httperror.Internal(err)
	}

	signURL := s.publicURL + "/s/" + token
	org := "Con Services"
	if st, err := s.settings.Get(ctx); err == nil && strings.TrimSpace(st.OrganizationName) != "" {
		org = st.OrganizationName
	}
	body := fmt.Sprintf(
		"Hello %s,\n\nPlease review and sign “%s” (version %d) for your onboarding at %s:\n\n%s\n\nThis link expires on %s.\n",
		signerName, doc.TemplateName, doc.TemplateVersion, org, signURL, expires.Format(time.RFC1123),
	)
	if err := s.mailer.Send(ctx, mail.Message{
		To: email, Subject: fmt.Sprintf("Please sign: %s", doc.TemplateName), Body: body,
	}); err != nil {
		return nil, httperror.Internal(fmt.Errorf("send sign email: %w", err))
	}
	if err := s.audit.Record(ctx, actor, "signing.send", "onboarding_document", documentID, map[string]any{
		"draft_id": draftID, "session_id": sessionID, "signer_email": email,
	}); err != nil {
		return nil, err
	}
	updated, err := s.getDocument(ctx, draftID, documentID)
	if err != nil {
		return nil, err
	}
	updated.SignURL = signURL
	return &SendResult{Document: updated, SignURL: signURL}, nil
}

// Waive marks a document waived when allow_stub_skip is on.
func (s *SigningService) Waive(ctx context.Context, actor string, draftID, documentID uuid.UUID, reason string) (*OnboardingDocument, error) {
	settings, err := s.settings.Get(ctx)
	if err != nil {
		return nil, err
	}
	if !settings.Signing.AllowStubSkip {
		return nil, httperror.Conflict("waiving a signing document is off; turn on Settings → Forms → Allow waiving documents without a provider for local testing")
	}
	reason = strings.TrimSpace(reason)
	if len(reason) > 500 {
		return nil, httperror.Validation("validation failed", map[string]string{"reason": "at most 500 characters"})
	}
	tag, err := s.pool.Exec(ctx, `
UPDATE onboarding_documents
   SET status = 'waived', status_note = $3, waived_by = $4, waived_at = now(), updated_at = now()
 WHERE id = $1 AND draft_id = $2`, documentID, draftID, reason, actor)
	if err != nil {
		return nil, httperror.Internal(err)
	}
	if tag.RowsAffected() == 0 {
		return nil, httperror.NotFound("onboarding document")
	}
	if err := s.audit.Record(ctx, actor, "signing.waive", "onboarding_document", documentID, map[string]any{
		"draft_id": draftID, "reason": reason,
	}); err != nil {
		return nil, err
	}
	doc, err := s.getDocument(ctx, draftID, documentID)
	if err != nil {
		return nil, err
	}
	_ = s.maybeNotifyReady(ctx, draftID)
	return doc, nil
}

// Unwaive reverts a waived document to pending.
func (s *SigningService) Unwaive(ctx context.Context, actor string, draftID, documentID uuid.UUID) (*OnboardingDocument, error) {
	tag, err := s.pool.Exec(ctx, `
UPDATE onboarding_documents
   SET status = 'pending', status_note = '', waived_by = '', waived_at = NULL, updated_at = now()
 WHERE id = $1 AND draft_id = $2`, documentID, draftID)
	if err != nil {
		return nil, httperror.Internal(err)
	}
	if tag.RowsAffected() == 0 {
		return nil, httperror.NotFound("onboarding document")
	}
	if err := s.audit.Record(ctx, actor, "signing.unwaive", "onboarding_document", documentID, map[string]any{
		"draft_id": draftID,
	}); err != nil {
		return nil, err
	}
	return s.getDocument(ctx, draftID, documentID)
}

func (s *SigningService) getDocument(ctx context.Context, draftID, documentID uuid.UUID) (*OnboardingDocument, error) {
	d := &OnboardingDocument{}
	err := s.pool.QueryRow(ctx, `
SELECT d.id, d.draft_id, d.template_id, t.name, COALESCE(v.version, 0), d.template_version_id,
       d.required, d.status, COALESCE(d.status_note, ''),
       COALESCE(d.waived_by, ''), d.waived_at, d.created_at, d.updated_at
  FROM onboarding_documents d
  JOIN document_templates t ON t.id = d.template_id
  LEFT JOIN document_template_versions v ON v.id = d.template_version_id
 WHERE d.id = $1 AND d.draft_id = $2`, documentID, draftID).
		Scan(&d.ID, &d.DraftID, &d.TemplateID, &d.TemplateName, &d.TemplateVersion, &d.VersionID,
			&d.Required, &d.Status, &d.StatusNote, &d.WaivedBy, &d.WaivedAt, &d.CreatedAt, &d.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, httperror.NotFound("onboarding document")
	}
	if err != nil {
		return nil, httperror.Internal(err)
	}
	return d, nil
}

// BlockingDocs returns attached documents that still block Approve/Complete.
// Any form pinned to the onboarding must be signed or waived first — not only
// those marked required on the template. Attaching means HR wants a signature
// before the IdP account is created.
func (s *SigningService) BlockingDocs(ctx context.Context, draftID uuid.UUID) ([]OnboardingDocument, error) {
	docs, err := s.ListForDraft(ctx, draftID)
	if err != nil {
		return nil, err
	}
	out := []OnboardingDocument{}
	for _, d := range docs {
		if d.Status != DocWaived && d.Status != DocSigned {
			out = append(out, d)
		}
	}
	return out, nil
}

// ----- public sign-hr -----

// GetPublicSession returns metadata for the sign-hr page.
func (s *SigningService) GetPublicSession(ctx context.Context, token string) (*PublicSigningSession, error) {
	row, err := s.sessionByToken(ctx, token)
	if err != nil {
		return nil, err
	}
	hasPDF := false
	if row.VersionID != nil && s.files != nil {
		var key string
		_ = s.pool.QueryRow(ctx, `SELECT storage_key FROM document_template_versions WHERE id = $1`, *row.VersionID).Scan(&key)
		hasPDF = key != ""
	}
	msg := ""
	if row.Status == "signed" {
		msg = "This form is already signed. HR will review your onboarding."
	}
	return &PublicSigningSession{
		FormName: row.FormName, FormVersion: row.FormVersion, SignerName: row.SignerName,
		Status: row.Status, HasPDF: hasPDF, ExpiresAt: row.ExpiresAt, Message: msg,
	}, nil
}

// PublicPDF returns the filled PDF bytes for a session token.
func (s *SigningService) PublicPDF(ctx context.Context, token string) ([]byte, string, error) {
	row, err := s.sessionByToken(ctx, token)
	if err != nil {
		return nil, "", err
	}
	if row.VersionID == nil {
		return nil, "", httperror.NotFound("form PDF")
	}
	return s.filledPDF(ctx, row.DocumentID, *row.VersionID)
}

// StaffPDF returns the filled PDF for an onboarding document (admin preview).
func (s *SigningService) StaffPDF(ctx context.Context, draftID, documentID uuid.UUID) ([]byte, string, error) {
	var versionID *uuid.UUID
	var gotDraft uuid.UUID
	err := s.pool.QueryRow(ctx, `
SELECT draft_id, template_version_id FROM onboarding_documents WHERE id = $1`, documentID).
		Scan(&gotDraft, &versionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", httperror.NotFound("document")
	}
	if err != nil {
		return nil, "", httperror.Internal(err)
	}
	if gotDraft != draftID {
		return nil, "", httperror.NotFound("document")
	}
	if versionID == nil {
		return nil, "", httperror.NotFound("form PDF")
	}
	return s.filledPDF(ctx, documentID, *versionID)
}

func (s *SigningService) filledPDF(ctx context.Context, documentID, versionID uuid.UUID) ([]byte, string, error) {
	if s.files == nil {
		return nil, "", httperror.NotFound("form PDF")
	}
	var key, fileName string
	if err := s.pool.QueryRow(ctx, `
SELECT storage_key, file_name FROM document_template_versions WHERE id = $1`, versionID).Scan(&key, &fileName); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, "", httperror.NotFound("form PDF")
		}
		return nil, "", httperror.Internal(err)
	}
	if key == "" {
		return nil, "", httperror.NotFound("form PDF")
	}
	raw, err := s.files.GetBytes(ctx, key)
	if err != nil {
		return nil, "", httperror.Internal(err)
	}
	person, err := s.personForDocument(ctx, documentID)
	if err != nil {
		return nil, "", err
	}
	fields, err := s.fieldsForVersion(ctx, versionID)
	if err != nil {
		return nil, "", err
	}
	values := mapPersonToFields(person, fields)
	filled, err := pdfform.Fill(raw, values)
	if err != nil {
		return nil, "", httperror.Internal(fmt.Errorf("fill form PDF from volunteer mappings: %w", err))
	}
	if fileName == "" {
		fileName = "form.pdf"
	}
	return filled, fileName, nil
}

// CancelOpenForDraft marks every open signing session for a draft cancelled so
// emailed links stop working after HR cancels the onboarding.
func (s *SigningService) CancelOpenForDraft(ctx context.Context, draftID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
UPDATE signing_sessions s
   SET status = 'cancelled', updated_at = now()
  FROM onboarding_documents d
 WHERE d.id = s.onboarding_document_id
   AND d.draft_id = $1
   AND s.status = 'sent'`, draftID)
	if err != nil {
		return httperror.Internal(err)
	}
	return nil
}

// SignPublicSession is the stub Sign action on sign-hr.
func (s *SigningService) SignPublicSession(ctx context.Context, token string) (*PublicSigningSession, error) {
	row, err := s.sessionByToken(ctx, token)
	if err != nil {
		return nil, err
	}
	if row.Status == "signed" {
		return s.GetPublicSession(ctx, token)
	}
	if row.Status != "sent" {
		return nil, httperror.Conflict("this signing link is no longer open")
	}
	if row.DraftState != string(domain.DraftStatePendingApproval) && row.DraftState != string(domain.DraftStateDraft) {
		return nil, httperror.Conflict("this onboarding is no longer waiting for signatures")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, httperror.Internal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `
UPDATE signing_sessions SET status = 'signed', completed_at = now(), updated_at = now()
 WHERE id = $1 AND status = 'sent'`, row.ID)
	if err != nil {
		return nil, httperror.Internal(err)
	}
	if tag.RowsAffected() == 0 {
		return nil, httperror.Conflict("this signing link is no longer open")
	}
	if _, err := tx.Exec(ctx, `
UPDATE onboarding_documents
   SET status = 'signed', status_note = $2, updated_at = now()
 WHERE id = $1`, row.DocumentID, SigningStubMessage); err != nil {
		return nil, httperror.Internal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, httperror.Internal(err)
	}
	_ = s.audit.Record(ctx, row.SignerName, "signing.signed_stub", "onboarding_document", row.DocumentID, map[string]any{
		"session_id": row.ID, "note": "sign-hr stub button",
	})
	_ = s.maybeNotifyReady(ctx, row.DraftID)
	return s.GetPublicSession(ctx, token)
}

type sessionRow struct {
	ID          uuid.UUID
	DocumentID  uuid.UUID
	DraftID     uuid.UUID
	VersionID   *uuid.UUID
	Status      string
	DraftState  string
	FormName    string
	FormVersion int
	SignerName  string
	ExpiresAt   time.Time
}

func (s *SigningService) sessionByToken(ctx context.Context, token string) (*sessionRow, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, httperror.NotFound("signing session")
	}
	r := &sessionRow{}
	err := s.pool.QueryRow(ctx, `
SELECT s.id, s.onboarding_document_id, d.draft_id, s.template_version_id, s.status,
       ed.state, s.form_name, s.form_version, s.signer_name, s.expires_at
  FROM signing_sessions s
  JOIN onboarding_documents d ON d.id = s.onboarding_document_id
  JOIN employment_drafts ed ON ed.id = d.draft_id
 WHERE s.token = $1`, token).
		Scan(&r.ID, &r.DocumentID, &r.DraftID, &r.VersionID, &r.Status, &r.DraftState, &r.FormName, &r.FormVersion, &r.SignerName, &r.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, httperror.NotFound("signing session")
	}
	if err != nil {
		return nil, httperror.Internal(err)
	}
	switch r.Status {
	case "sent", "signed":
		// ok for read; sign handler further restricts writes to "sent"
	default:
		return nil, httperror.NotFound("signing session")
	}
	if r.DraftState == string(domain.DraftStateRejected) || r.DraftState == string(domain.DraftStateActive) {
		return nil, httperror.NotFound("signing session")
	}
	if time.Now().UTC().After(r.ExpiresAt) {
		if r.Status == "sent" {
			_, _ = s.pool.Exec(ctx, `UPDATE signing_sessions SET status = 'expired', updated_at = now() WHERE id = $1 AND status = 'sent'`, r.ID)
		}
		return nil, httperror.Conflict("this signing link has expired; ask HR to send the form again")
	}
	return r, nil
}

func (s *SigningService) maybeNotifyReady(ctx context.Context, draftID uuid.UUID) error {
	blocking, err := s.BlockingDocs(ctx, draftID)
	if err != nil || len(blocking) > 0 || s.notify == nil {
		return err
	}
	var title string
	_ = s.pool.QueryRow(ctx, `SELECT title FROM employment_drafts WHERE id = $1`, draftID).Scan(&title)
	link := s.appBase
	if link != "" {
		// SPA routes live under /onboarding/... (TanStack's "app" id is not a URL segment).
		link = link + "/onboarding/" + draftID.String() + "/review"
	}
	body := fmt.Sprintf(
		"All attached onboarding forms are signed or waived for draft %q (%s).\n\nOpen Onboarding → Ready to approve (not the Review queue), check the signatures yourself, then Approve to create the volunteer account.\n%s\n",
		title, draftID.String(), link,
	)
	return s.notify.Send(ctx, "Onboarding ready to approve: "+title, body)
}

func (s *SigningService) personForDraft(ctx context.Context, draftID uuid.UUID) (*domain.Person, error) {
	var personID uuid.UUID
	if err := s.pool.QueryRow(ctx, `SELECT person_id FROM employment_drafts WHERE id = $1`, draftID).Scan(&personID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, httperror.NotFound("onboarding")
		}
		return nil, httperror.Internal(err)
	}
	return s.loadPerson(ctx, personID)
}

func (s *SigningService) personForDocument(ctx context.Context, documentID uuid.UUID) (*domain.Person, error) {
	var draftID uuid.UUID
	if err := s.pool.QueryRow(ctx, `SELECT draft_id FROM onboarding_documents WHERE id = $1`, documentID).Scan(&draftID); err != nil {
		return nil, httperror.Internal(err)
	}
	return s.personForDraft(ctx, draftID)
}

func (s *SigningService) loadPerson(ctx context.Context, id uuid.UUID) (*domain.Person, error) {
	p := &domain.Person{}
	var addressJSON []byte
	err := s.pool.QueryRow(ctx, `
SELECT id, COALESCE(first_name,''), COALESCE(last_name,''), COALESCE(nickname,''), COALESCE(username,''),
       COALESCE(date_of_birth::text,''), COALESCE(legal_name,''), COALESCE(display_name,''),
       COALESCE(email,''), COALESCE(org_email,''), COALESCE(phone,''), COALESCE(address, '{}'::jsonb)
  FROM people WHERE id = $1`, id).
		Scan(&p.ID, &p.FirstName, &p.LastName, &p.Nickname, &p.Username, &p.DateOfBirth,
			&p.LegalName, &p.DisplayName, &p.Email, &p.OrgEmail, &p.Phone, &addressJSON)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, httperror.NotFound("person")
	}
	if err != nil {
		return nil, httperror.Internal(err)
	}
	_ = json.Unmarshal(addressJSON, &p.Address)
	return p, nil
}

func (s *SigningService) fieldsForVersion(ctx context.Context, versionID uuid.UUID) ([]DocumentTemplateField, error) {
	rows, err := s.pool.Query(ctx, `
SELECT id, pdf_field_name, field_type, hr_attribute
  FROM document_template_fields WHERE template_version_id = $1`, versionID)
	if err != nil {
		return nil, httperror.Internal(err)
	}
	defer rows.Close()
	out := []DocumentTemplateField{}
	for rows.Next() {
		var f DocumentTemplateField
		if err := rows.Scan(&f.ID, &f.PDFFieldName, &f.FieldType, &f.HRAttribute); err != nil {
			return nil, httperror.Internal(err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func mapPersonToFields(p *domain.Person, fields []DocumentTemplateField) map[string]string {
	attrs := map[string]string{
		"first_name":          p.FirstName,
		"last_name":           p.LastName,
		"legal_name":          p.LegalName,
		"display_name":        p.DisplayName,
		"nickname":            p.Nickname,
		"username":            p.Username,
		"email":               p.Email,
		"org_email":           p.OrgEmail,
		"phone":               p.Phone,
		"date_of_birth":       p.DateOfBirth,
		"address_line":        p.Address.Line(),
		"address_street":      p.Address.Street,
		"address_city":        p.Address.City,
		"address_postal_code": p.Address.PostalCode,
		"address_country":     p.Address.Country,
	}
	out := map[string]string{}
	for _, f := range fields {
		if f.HRAttribute == "" {
			continue
		}
		if v, ok := attrs[f.HRAttribute]; ok && v != "" {
			out[f.PDFFieldName] = v
		}
	}
	return out
}

func randomToken(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
