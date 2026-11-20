package http

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/AGlElectronics/hr/backend/internal/authz"
	"github.com/AGlElectronics/hr/backend/internal/httperror"
	"github.com/AGlElectronics/hr/backend/internal/service"
)

// signingHandler groups forms library, onboarding documents, and public sign-hr APIs.
type signingHandler struct {
	svc *service.SigningService
	chk *authz.Checker
}

// ----- templates (Settings → Forms) -----

func (h *signingHandler) listTemplates(w http.ResponseWriter, r *http.Request) {
	items, err := h.svc.ListTemplates(r.Context())
	if !ok(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *signingHandler) getTemplate(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "id")
	if !ok(w, r, err) {
		return
	}
	t, err := h.svc.GetTemplate(r.Context(), id)
	if !ok(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (h *signingHandler) createTemplate(w http.ResponseWriter, r *http.Request) {
	var in service.DocumentTemplateInput
	if !ok(w, r, decodeJSON(r, &in)) {
		return
	}
	t, err := h.svc.CreateTemplate(r.Context(), actorFrom(r), in)
	if !ok(w, r, err) {
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

func (h *signingHandler) updateTemplate(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "id")
	if !ok(w, r, err) {
		return
	}
	var in service.DocumentTemplateInput
	if !ok(w, r, decodeJSON(r, &in)) {
		return
	}
	t, err := h.svc.UpdateTemplate(r.Context(), actorFrom(r), id, in)
	if !ok(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (h *signingHandler) deleteTemplate(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "id")
	if !ok(w, r, err) || !ok(w, r, h.svc.DeleteTemplate(r.Context(), actorFrom(r), id)) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *signingHandler) listVersions(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "id")
	if !ok(w, r, err) {
		return
	}
	items, err := h.svc.ListVersions(r.Context(), id)
	if !ok(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// inspectPDF reads an uploaded PDF and returns its AcroForm field names/types.
func (h *signingHandler) inspectPDF(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(20 << 20); !ok(w, r, err) {
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		_ = ok(w, r, httperror.Validation("validation failed", map[string]string{"file": "required"}))
		return
	}
	defer func() { _ = file.Close() }()
	pdfBytes, err := io.ReadAll(file)
	if !ok(w, r, err) {
		return
	}
	fields, err := h.svc.InspectPDF(pdfBytes)
	if !ok(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"fields": fields})
}

// publishVersion accepts multipart: file=PDF, change_note, fields=JSON array.
func (h *signingHandler) publishVersion(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "id")
	if !ok(w, r, err) {
		return
	}
	if err := r.ParseMultipartForm(20 << 20); !ok(w, r, err) {
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		_ = ok(w, r, httperror.Validation("validation failed", map[string]string{"file": "required"}))
		return
	}
	defer func() { _ = file.Close() }()
	pdfBytes, err := io.ReadAll(file)
	if !ok(w, r, err) {
		return
	}
	var fields []service.FieldMappingInput
	if raw := r.FormValue("fields"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &fields); !ok(w, r, err) {
			return
		}
	}
	v, err := h.svc.PublishVersion(r.Context(), actorFrom(r), id, hdr.Filename, pdfBytes, fields, r.FormValue("change_note"))
	if !ok(w, r, err) {
		return
	}
	writeJSON(w, http.StatusCreated, v)
}

// ----- onboarding documents -----

func (h *signingHandler) listForOnboarding(w http.ResponseWriter, r *http.Request) {
	draftID, err := parseUUIDParam(r, "id")
	if !ok(w, r, err) || !ok(w, r, h.chk.Draft(r.Context(), authz.PeopleRead, draftID)) {
		return
	}
	items, err := h.svc.ListForDraft(r.Context(), draftID)
	if !ok(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *signingHandler) attach(w http.ResponseWriter, r *http.Request) {
	draftID, err := parseUUIDParam(r, "id")
	if !ok(w, r, err) || !ok(w, r, h.chk.Draft(r.Context(), authz.OnboardingManage, draftID)) {
		return
	}
	var body struct {
		TemplateID string `json:"template_id"`
	}
	if !ok(w, r, decodeJSON(r, &body)) {
		return
	}
	tid, err := uuid.Parse(body.TemplateID)
	if err != nil {
		_ = ok(w, r, httperror.Validation("validation failed", map[string]string{"template_id": "must be a UUID"}))
		return
	}
	d, err := h.svc.AttachTemplate(r.Context(), actorFrom(r), draftID, tid)
	if !ok(w, r, err) {
		return
	}
	writeJSON(w, http.StatusCreated, d)
}

func (h *signingHandler) detach(w http.ResponseWriter, r *http.Request) {
	draftID, err := parseUUIDParam(r, "id")
	if !ok(w, r, err) || !ok(w, r, h.chk.Draft(r.Context(), authz.OnboardingManage, draftID)) {
		return
	}
	docID, err := parseUUIDParam(r, "docID")
	if !ok(w, r, err) || !ok(w, r, h.svc.DetachDocument(r.Context(), actorFrom(r), draftID, docID)) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *signingHandler) send(w http.ResponseWriter, r *http.Request) {
	draftID, err := parseUUIDParam(r, "id")
	if !ok(w, r, err) || !ok(w, r, h.chk.Draft(r.Context(), authz.OnboardingManage, draftID)) {
		return
	}
	docID, err := parseUUIDParam(r, "docID")
	if !ok(w, r, err) {
		return
	}
	res, err := h.svc.SendForSigning(r.Context(), actorFrom(r), draftID, docID)
	if !ok(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *signingHandler) waive(w http.ResponseWriter, r *http.Request) {
	draftID, err := parseUUIDParam(r, "id")
	if !ok(w, r, err) || !ok(w, r, h.chk.Draft(r.Context(), authz.OnboardingManage, draftID)) {
		return
	}
	docID, err := parseUUIDParam(r, "docID")
	if !ok(w, r, err) {
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if r.ContentLength != 0 && !ok(w, r, decodeJSON(r, &body)) {
		return
	}
	d, err := h.svc.Waive(r.Context(), actorFrom(r), draftID, docID, body.Reason)
	if !ok(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (h *signingHandler) unwaive(w http.ResponseWriter, r *http.Request) {
	draftID, err := parseUUIDParam(r, "id")
	if !ok(w, r, err) || !ok(w, r, h.chk.Draft(r.Context(), authz.OnboardingManage, draftID)) {
		return
	}
	docID, err := parseUUIDParam(r, "docID")
	if !ok(w, r, err) {
		return
	}
	d, err := h.svc.Unwaive(r.Context(), actorFrom(r), draftID, docID)
	if !ok(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// staffPDF serves the filled form for HR to preview on Review (inline by default).
func (h *signingHandler) staffPDF(w http.ResponseWriter, r *http.Request) {
	draftID, err := parseUUIDParam(r, "id")
	if !ok(w, r, err) || !ok(w, r, h.chk.Draft(r.Context(), authz.PeopleRead, draftID)) {
		return
	}
	docID, err := parseUUIDParam(r, "docID")
	if !ok(w, r, err) {
		return
	}
	pdf, name, err := h.svc.StaffPDF(r.Context(), draftID, docID)
	if !ok(w, r, err) {
		return
	}
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'self'")
	w.Header().Set("Content-Type", "application/pdf")
	disp := "inline"
	if r.URL.Query().Get("download") == "1" {
		disp = "attachment"
	}
	w.Header().Set("Content-Disposition", contentDisposition(disp, name))
	w.Header().Set("Content-Length", strconv.Itoa(len(pdf)))
	_, _ = w.Write(pdf)
}

// ----- public sign-hr (token in path; no staff OIDC) -----

func (h *signingHandler) publicGet(w http.ResponseWriter, r *http.Request) {
	s, err := h.svc.GetPublicSession(r.Context(), chi.URLParam(r, "token"))
	if !ok(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, s)
}

func (h *signingHandler) publicPDF(w http.ResponseWriter, r *http.Request) {
	pdf, name, err := h.svc.PublicPDF(r.Context(), chi.URLParam(r, "token"))
	if !ok(w, r, err) {
		return
	}
	// Allow the sign-hr page (same host) to show the PDF in an iframe.
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'self'")
	w.Header().Set("Content-Type", "application/pdf")
	disp := "inline"
	if r.URL.Query().Get("download") == "1" {
		disp = "attachment"
	}
	w.Header().Set("Content-Disposition", contentDisposition(disp, name))
	w.Header().Set("Content-Length", strconv.Itoa(len(pdf)))
	_, _ = w.Write(pdf)
}

func (h *signingHandler) publicSign(w http.ResponseWriter, r *http.Request) {
	s, err := h.svc.SignPublicSession(r.Context(), chi.URLParam(r, "token"))
	if !ok(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, s)
}
