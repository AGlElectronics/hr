package http

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/AGlElectronics/hr/backend/internal/httperror"
	"github.com/AGlElectronics/hr/backend/internal/importer"
	"github.com/AGlElectronics/hr/backend/internal/service"
)

type importHandler struct {
	svc *service.ImportService
}

const xlsxType = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"

// upload reads the multipart "file" (at most importer.MaxBytes) and the
// JSON "options" field.
func upload(w http.ResponseWriter, r *http.Request) (name string, data []byte, opts service.ImportOptions, good bool) {
	r.Body = http.MaxBytesReader(w, r.Body, importer.MaxBytes+1<<20)
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		httperror.Render(w, r, httperror.BadRequest("send the file as multipart/form-data (at most 10 MB): "+err.Error()))
		return "", nil, opts, false
	}
	f, hdr, err := r.FormFile("file")
	if err != nil {
		httperror.Render(w, r, httperror.Validation("validation failed", map[string]string{"file": "required"}))
		return "", nil, opts, false
	}
	defer func() { _ = f.Close() }()
	data, err = io.ReadAll(io.LimitReader(f, importer.MaxBytes+1))
	if err != nil {
		httperror.Render(w, r, httperror.BadRequest(err.Error()))
		return "", nil, opts, false
	}
	if raw := r.FormValue("options"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &opts); err != nil {
			httperror.Render(w, r, httperror.BadRequest("options must be JSON: "+err.Error()))
			return "", nil, opts, false
		}
	}
	return hdr.Filename, data, opts, true
}

func (h *importHandler) inspect(w http.ResponseWriter, r *http.Request) {
	name, data, opts, good := upload(w, r)
	if !good {
		return
	}
	res, err := h.svc.Inspect(name, data, opts.Sheet, opts.HeaderRow)
	if !ok(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// preview is the dry run; ?report=xlsx returns the error report instead.
func (h *importHandler) preview(w http.ResponseWriter, r *http.Request) {
	name, data, opts, good := upload(w, r)
	if !good {
		return
	}
	res, err := h.svc.Preview(r.Context(), name, data, opts)
	if !ok(w, r, err) {
		return
	}
	if r.URL.Query().Get("report") == "xlsx" {
		report, err := service.ReportFromPreview(res)
		if !ok(w, r, err) {
			return
		}
		sendXLSX(w, strings.TrimSuffix(name, ".csv")+"-problems.xlsx", report)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *importHandler) commit(w http.ResponseWriter, r *http.Request) {
	name, data, opts, good := upload(w, r)
	if !good {
		return
	}
	b, err := h.svc.Commit(r.Context(), actorFrom(r), name, data, opts)
	if !ok(w, r, err) {
		return
	}
	writeJSON(w, http.StatusCreated, b)
}

func (h *importHandler) list(w http.ResponseWriter, r *http.Request) {
	items, err := h.svc.Batches(r.Context())
	if !ok(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *importHandler) get(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "id")
	if !ok(w, r, err) {
		return
	}
	b, err := h.svc.Batch(r.Context(), id)
	if !ok(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, b)
}

func (h *importHandler) errors(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "id")
	if !ok(w, r, err) {
		return
	}
	b, err := h.svc.Batch(r.Context(), id)
	if !ok(w, r, err) {
		return
	}
	report, err := service.ReportFromBatch(b)
	if !ok(w, r, err) {
		return
	}
	sendXLSX(w, "import-"+strconv.Itoa(b.RowsFailed)+"-problems.xlsx", report)
}

func (h *importHandler) undo(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "id")
	if !ok(w, r, err) {
		return
	}
	b, err := h.svc.Undo(r.Context(), actorFrom(r), id)
	if !ok(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, b)
}

func (h *importHandler) templates(w http.ResponseWriter, r *http.Request) {
	items, err := h.svc.Templates(r.Context())
	if !ok(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *importHandler) saveTemplate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name    string                `json:"name"`
		Mapping service.ImportOptions `json:"mapping"`
	}
	if !ok(w, r, decodeJSON(r, &in)) {
		return
	}
	t, err := h.svc.SaveTemplate(r.Context(), actorFrom(r), in.Name, in.Mapping)
	if !ok(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (h *importHandler) deleteTemplate(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "id")
	if !ok(w, r, err) || !ok(w, r, h.svc.DeleteTemplate(r.Context(), actorFrom(r), id)) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func sendXLSX(w http.ResponseWriter, name string, data []byte) {
	w.Header().Set("Content-Type", xlsxType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+strings.ReplaceAll(name, `"`, "")+`"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
