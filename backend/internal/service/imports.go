package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AGlElectronics/hr/backend/internal/audit"
	"github.com/AGlElectronics/hr/backend/internal/domain"
	"github.com/AGlElectronics/hr/backend/internal/httperror"
	"github.com/AGlElectronics/hr/backend/internal/importer"
)

// ImportService loads spreadsheets into the review queue. Rows never become
// people directly: valid rows are review items an HR admin or lead still
// accepts. Re-running an import updates items (matched on email or the id
// column) and never deletes; a batch can be undone while none of its items
// was accepted. The uploaded file is not stored, only the mapped fields.
type ImportService struct {
	pool     *pgxpool.Pool
	audit    *audit.Writer
	settings *SettingsService
}

// NewImportService wires an ImportService.
func NewImportService(pool *pgxpool.Pool, aud *audit.Writer, settings *SettingsService) *ImportService {
	return &ImportService{pool: pool, audit: aud, settings: settings}
}

// ImportOptions say how to read the file.
type ImportOptions struct {
	Sheet     string `json:"sheet,omitempty"`
	HeaderRow int    `json:"header_row,omitempty"`
	// Columns maps HR fields to the file's headers.
	Columns map[string]string `json:"columns"`
	// DepartmentValues maps values of the department column ("Sec") to
	// departments; values matching a department's name or slug need none.
	DepartmentValues    map[string]uuid.UUID `json:"department_values,omitempty"`
	DefaultDepartmentID *uuid.UUID           `json:"default_department_id,omitempty"`
}

// InspectResult describes a file before mapping.
type InspectResult struct {
	Format    string            `json:"format"`
	Encoding  string            `json:"encoding,omitempty"`
	Delimiter string            `json:"delimiter,omitempty"`
	Sheets    []string          `json:"sheets"`
	Sheet     string            `json:"sheet"`
	HeaderRow int               `json:"header_row"`
	Headers   []string          `json:"headers"`
	Sample    [][]string        `json:"sample"`
	Rows      int               `json:"rows"`
	Suggested map[string]string `json:"suggested_columns"`
	Fields    []string          `json:"fields"`
}

func pickSheet(wb *importer.Workbook, name string) (importer.Sheet, error) {
	if name == "" {
		return wb.Sheets[0], nil
	}
	for _, s := range wb.Sheets {
		if s.Name == name {
			return s, nil
		}
	}
	return importer.Sheet{}, httperror.Validation("validation failed", map[string]string{"sheet": "no sheet named " + name})
}

func (s *ImportService) table(name string, data []byte, sheet string, headerRow int) (*importer.Workbook, importer.Sheet, *importer.Table, error) {
	wb, err := importer.Parse(name, data)
	if err != nil {
		return nil, importer.Sheet{}, nil, httperror.Validation(err.Error(), map[string]string{"file": err.Error()})
	}
	if len(wb.Sheets) == 0 {
		return nil, importer.Sheet{}, nil, httperror.Validation("the file has no sheets", nil)
	}
	sh, err := pickSheet(wb, sheet)
	if err != nil {
		return nil, importer.Sheet{}, nil, err
	}
	tbl, err := sh.Table(headerRow)
	if err != nil {
		return nil, importer.Sheet{}, nil, httperror.Validation(err.Error(), map[string]string{"header_row": err.Error()})
	}
	return wb, sh, tbl, nil
}

// Inspect lists the sheets and headers of a file with suggested columns.
func (s *ImportService) Inspect(name string, data []byte, sheet string, headerRow int) (*InspectResult, error) {
	wb, sh, tbl, err := s.table(name, data, sheet, headerRow)
	if err != nil {
		return nil, err
	}
	out := &InspectResult{Format: wb.Format, Encoding: wb.Encoding, Delimiter: wb.Delimiter, Sheet: sh.Name, Headers: tbl.Headers,
		Rows: len(tbl.Rows), Suggested: importer.Suggest(tbl.Headers), Fields: importer.Fields, Sample: [][]string{}}
	for _, ws := range wb.Sheets {
		out.Sheets = append(out.Sheets, ws.Name)
	}
	if len(tbl.RowNumbers) > 0 {
		out.HeaderRow = tbl.RowNumbers[0] - 1
	}
	for i := 0; i < len(tbl.Rows) && i < 5; i++ {
		row := []string{}
		for _, c := range tbl.Rows[i] {
			row = append(row, c.Value)
		}
		out.Sample = append(out.Sample, row)
	}
	return out, nil
}

// PreviewRow is the dry-run result for one row.
type PreviewRow struct {
	Row    int    `json:"row"`
	Status string `json:"status"` // new | update | unchanged | error | duplicate
	Email  string `json:"email"`
	Name   string `json:"name"`
	// Problems keep the row out of the import; Warnings are imported and
	// shown to the reviewer.
	Problems      []string    `json:"problems"`
	Warnings      []string    `json:"warnings"`
	MatchPersonID *uuid.UUID  `json:"match_person_id,omitempty"`
	ReviewItemID  *uuid.UUID  `json:"review_item_id,omitempty"`
	DepartmentID  *uuid.UUID  `json:"department_id,omitempty"`
	BundleIDs     []uuid.UUID `json:"bundle_ids"`

	rec    importRecord
	values map[string]string
}

// PreviewResult is the dry run of a whole file.
type PreviewResult struct {
	Rows    []PreviewRow   `json:"rows"`
	Summary map[string]int `json:"summary"`
}

type importRecord struct {
	FirstName, LastName, Nickname, Email, Phone, ExternalRef string
	Data                                                     ReviewData
}

func (s *ImportService) departments(ctx context.Context) (map[string]uuid.UUID, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, name, slug FROM departments WHERE archived_at IS NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		var name, slug string
		if err := rows.Scan(&id, &name, &slug); err != nil {
			return nil, err
		}
		out[strings.ToLower(name)], out[slug] = id, id
	}
	return out, rows.Err()
}

func (s *ImportService) bundleNames(ctx context.Context) (map[string]uuid.UUID, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, name FROM access_bundles`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		out[strings.ToLower(name)] = id
	}
	return out, rows.Err()
}

// Preview is the dry run: what each row would do, without writing.
func (s *ImportService) Preview(ctx context.Context, name string, data []byte, opts ImportOptions) (*PreviewResult, error) {
	_, _, tbl, err := s.table(name, data, opts.Sheet, opts.HeaderRow)
	if err != nil {
		return nil, err
	}
	if opts.Columns["email"] == "" {
		return nil, httperror.Validation("validation failed", map[string]string{"columns.email": "map a column to email; it is used to contact and match people"})
	}
	for field, header := range opts.Columns {
		found := false
		for _, h := range tbl.Headers {
			found = found || h == header
		}
		if header != "" && !found {
			return nil, httperror.Validation("validation failed", map[string]string{"columns." + field: "no column named " + header})
		}
	}
	st, err := s.settings.Get(ctx)
	if err != nil {
		return nil, err
	}
	depts, err := s.departments(ctx)
	if err != nil {
		return nil, httperror.Internal(err)
	}
	for k, v := range opts.DepartmentValues {
		depts[strings.ToLower(strings.TrimSpace(k))] = v
	}
	bundles, err := s.bundleNames(ctx)
	if err != nil {
		return nil, httperror.Internal(err)
	}
	res := &PreviewResult{Rows: []PreviewRow{}, Summary: map[string]int{"new": 0, "update": 0, "unchanged": 0, "error": 0, "duplicate": 0}}
	seen := map[string]int{}
	for i := range tbl.Rows {
		row := s.readRow(tbl, i, opts, st.DefaultCountry, depts, bundles)
		switch {
		case len(row.Problems) > 0:
			row.Status = "error"
		case seen[row.Email] > 0:
			row.Status = "duplicate"
			row.Problems = append(row.Problems, fmt.Sprintf("same email as row %d", seen[row.Email]))
		default:
			seen[row.Email] = row.Row
			if err := s.classify(ctx, &row); err != nil {
				return nil, httperror.Internal(err)
			}
		}
		res.Summary[row.Status]++
		res.Rows = append(res.Rows, row)
	}
	return res, nil
}

func (s *ImportService) readRow(tbl *importer.Table, i int, opts ImportOptions, country string, depts, bundles map[string]uuid.UUID) PreviewRow {
	get := func(field string) importer.Cell {
		if h := opts.Columns[field]; h != "" {
			return tbl.Value(i, h)
		}
		return importer.Cell{}
	}
	row := PreviewRow{Row: tbl.RowNumbers[i], Problems: []string{}, Warnings: []string{}, BundleIDs: []uuid.UUID{}, values: map[string]string{}}
	for field := range opts.Columns {
		row.values[field] = get(field).Value
	}
	r := &row.rec
	r.FirstName, r.LastName, r.Nickname = cleanName(get("first_name").Value), cleanName(get("last_name").Value), cleanName(get("nickname").Value)
	if full := cleanName(get("full_name").Value); full != "" && r.FirstName == "" && r.LastName == "" {
		parts := strings.Fields(full)
		r.FirstName = parts[0]
		r.LastName = strings.Join(parts[1:], " ")
		row.Warnings = append(row.Warnings, fmt.Sprintf("name %q split into first name %q and last name %q; check it", full, r.FirstName, r.LastName))
	}
	email, cleaned := importer.CleanEmail(get("email").Value)
	switch {
	case email == "":
		row.Problems = append(row.Problems, "email is missing")
	case !validEmail(email):
		row.Problems = append(row.Problems, fmt.Sprintf("%q is not a valid email address", get("email").Value))
	case cleaned:
		row.Warnings = append(row.Warnings, fmt.Sprintf("email %q cleaned to %s", get("email").Value, email))
	}
	r.Email, row.Email = email, email
	row.Name = strings.TrimSpace(r.FirstName + " " + r.LastName)
	if org, _ := importer.CleanEmail(get("org_email").Value); org != "" {
		r.Data.OrgEmail = org
	}
	for _, f := range []struct {
		field string
		dst   *string
	}{{"phone", &r.Phone}, {"emergency_phone", &r.Data.EmergencyContact.Phone}} {
		c := get(f.field)
		if c.Value == "" {
			continue
		}
		if w := importer.PhoneWarning(c); w != "" {
			row.Warnings = append(row.Warnings, w)
		}
		raw := importer.PhoneDigits(c)
		if e164 := NormalizePhone(raw, country); e164 != "" {
			*f.dst = e164
		} else {
			*f.dst = raw
			row.Warnings = append(row.Warnings, fmt.Sprintf("%s %q is not a valid phone number; fix it before accepting", strings.ReplaceAll(f.field, "_", " "), c.Value))
		}
	}
	if d, note, err := importer.ParseDate(get("date_of_birth")); err != nil {
		row.Warnings = append(row.Warnings, "date of birth: "+err.Error())
	} else {
		r.Data.DateOfBirth = d
		if note != "" {
			row.Warnings = append(row.Warnings, note)
		}
	}
	r.Data.Address = normalizeAddress(domain.Address{Street: get("street").Value, PostalCode: get("postal_code").Value,
		City: get("city").Value, Country: get("country").Value})
	r.Data.EmergencyContact.Name = cleanName(get("emergency_name").Value)
	if e, _ := importer.CleanEmail(get("emergency_email").Value); e != "" {
		r.Data.EmergencyContact.Email = e
	}
	r.Data.Notes = get("notes").Value
	r.ExternalRef = get("external_id").Value
	if v := get("department").Value; v != "" {
		vals := importer.SplitValues(v)
		if id, ok := depts[strings.ToLower(vals[0])]; ok {
			row.DepartmentID = &id
		} else {
			row.Warnings = append(row.Warnings, fmt.Sprintf("department %q is not mapped to an HR department", vals[0]))
		}
		if len(vals) > 1 {
			row.Warnings = append(row.Warnings, fmt.Sprintf("several departments (%s); only %q is used", v, vals[0]))
		}
	}
	if row.DepartmentID == nil && opts.DefaultDepartmentID != nil {
		row.DepartmentID = opts.DefaultDepartmentID
	}
	for _, v := range importer.SplitValues(get("roles").Value) {
		if id, ok := bundles[strings.ToLower(v)]; ok {
			row.BundleIDs = append(row.BundleIDs, id)
			continue
		}
		r.Data.Roles = append(r.Data.Roles, v)
		row.Warnings = append(row.Warnings, fmt.Sprintf("role %q is not an access group; the reviewer assigns it by hand", v))
	}
	r.Data.Warnings = row.Warnings
	return row
}

// classify finds the open import item (same email or id) or person a row
// matches.
func (s *ImportService) classify(ctx context.Context, row *PreviewRow) error {
	var id uuid.UUID
	var first, last, nick, phone string
	var data []byte
	var dept *uuid.UUID
	err := s.pool.QueryRow(ctx, `
SELECT id, first_name, last_name, nickname, phone, data, department_id FROM review_items
 WHERE source = 'import' AND status IN ('pending', 'ready')
   AND (lower(email) = $1 OR ($2 <> '' AND external_ref = $2))
 ORDER BY created_at LIMIT 1`, row.Email, row.rec.ExternalRef).Scan(&id, &first, &last, &nick, &phone, &data, &dept)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		row.Status = "new"
	case err != nil:
		return err
	default:
		row.ReviewItemID = &id
		var old ReviewData
		_ = json.Unmarshal(data, &old)
		newData, _ := json.Marshal(mergeReviewData(old, row.rec.Data))
		oldData, _ := json.Marshal(old)
		same := first == firstNonEmpty(row.rec.FirstName, first) && last == firstNonEmpty(row.rec.LastName, last) &&
			nick == firstNonEmpty(row.rec.Nickname, nick) && phone == firstNonEmpty(row.rec.Phone, phone) &&
			string(newData) == string(oldData) && (row.DepartmentID == nil || (dept != nil && *dept == *row.DepartmentID))
		row.Status = map[bool]string{true: "unchanged", false: "update"}[same]
	}
	var person uuid.UUID
	err = s.pool.QueryRow(ctx, `SELECT id FROM people WHERE lower(email) = $1 OR lower(COALESCE(org_email, '')) = $1 LIMIT 1`, row.Email).Scan(&person)
	if err == nil {
		row.MatchPersonID = &person
		row.Warnings = append(row.Warnings, "an HR person with this email already exists; merge instead of accepting")
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	return nil
}

// mergeReviewData fills old with the non-empty values of next.
func mergeReviewData(old, next ReviewData) ReviewData {
	out := old
	set := func(dst *string, v string) {
		if v != "" {
			*dst = v
		}
	}
	set(&out.DateOfBirth, next.DateOfBirth)
	set(&out.OrgEmail, next.OrgEmail)
	set(&out.Notes, next.Notes)
	set(&out.Address.Street, next.Address.Street)
	set(&out.Address.PostalCode, next.Address.PostalCode)
	set(&out.Address.City, next.Address.City)
	set(&out.Address.Country, next.Address.Country)
	set(&out.EmergencyContact.Name, next.EmergencyContact.Name)
	set(&out.EmergencyContact.Phone, next.EmergencyContact.Phone)
	set(&out.EmergencyContact.Email, next.EmergencyContact.Email)
	if len(next.Roles) > 0 {
		out.Roles = next.Roles
	}
	out.Warnings = next.Warnings
	return out
}

// ImportBatch is one committed import.
type ImportBatch struct {
	ID          uuid.UUID        `json:"id"`
	FileName    string           `json:"file_name"`
	CreatedBy   string           `json:"created_by"`
	CreatedAt   time.Time        `json:"created_at"`
	RowsTotal   int              `json:"rows_total"`
	RowsCreated int              `json:"rows_created"`
	RowsUpdated int              `json:"rows_updated"`
	RowsFailed  int              `json:"rows_failed"`
	Errors      []map[string]any `json:"errors,omitempty"`
	UndoneAt    *time.Time       `json:"undone_at,omitempty"`
	// Open counts items of the batch still waiting for review.
	Open int `json:"open"`
}

func readyStatus(r importRecord) string {
	p := PersonInput{FirstName: r.FirstName, LastName: r.LastName, Nickname: r.Nickname, Email: r.Email, Phone: r.Phone,
		DateOfBirth: r.Data.DateOfBirth, Address: r.Data.Address, EmergencyContact: r.Data.EmergencyContact}
	if len(p.normalize(rules{})) == 0 {
		return ReviewReady
	}
	return ReviewPending
}

// Commit imports the valid rows into the review queue as one batch.
func (s *ImportService) Commit(ctx context.Context, actor, name string, data []byte, opts ImportOptions) (*ImportBatch, error) {
	prev, err := s.Preview(ctx, name, data, opts)
	if err != nil {
		return nil, err
	}
	var batchID uuid.UUID
	if err := s.pool.QueryRow(ctx, `INSERT INTO import_batches (file_name, created_by, rows_total) VALUES ($1, $2, $3) RETURNING id`,
		name, actor, len(prev.Rows)).Scan(&batchID); err != nil {
		return nil, httperror.Internal(err)
	}
	created, updated := 0, 0
	errRows := []map[string]any{}
	for _, row := range prev.Rows {
		r := row.rec
		raw, _ := json.Marshal(r.Data)
		bundles, _ := json.Marshal(row.BundleIDs)
		switch row.Status {
		case "error", "duplicate":
			vals := map[string]any{}
			for k, v := range row.values {
				vals[k] = v
			}
			errRows = append(errRows, map[string]any{"row": row.Row, "values": vals, "problem": strings.Join(row.Problems, "; ")})
			continue
		case "unchanged":
			continue
		case "update":
			var old []byte
			if err := s.pool.QueryRow(ctx, `SELECT data FROM review_items WHERE id = $1`, *row.ReviewItemID).Scan(&old); err != nil {
				return nil, httperror.Internal(err)
			}
			var od ReviewData
			_ = json.Unmarshal(old, &od)
			merged, _ := json.Marshal(mergeReviewData(od, r.Data))
			if _, err := s.pool.Exec(ctx, `
UPDATE review_items SET first_name = COALESCE(NULLIF($2, ''), first_name), last_name = COALESCE(NULLIF($3, ''), last_name),
       nickname = COALESCE(NULLIF($4, ''), nickname), phone = COALESCE(NULLIF($5, ''), phone), data = $6,
       department_id = COALESCE($7, department_id), external_ref = COALESCE(NULLIF($8, ''), external_ref),
       bundle_ids = CASE WHEN $9::jsonb = '[]'::jsonb THEN bundle_ids ELSE $9::jsonb END,
       match_person_id = COALESCE($10, match_person_id), updated_at = now()
 WHERE id = $1`, *row.ReviewItemID, r.FirstName, r.LastName, r.Nickname, r.Phone, merged, row.DepartmentID, r.ExternalRef,
				bundles, row.MatchPersonID); err != nil {
				return nil, httperror.Internal(err)
			}
			updated++
		default:
			kind := ""
			if row.MatchPersonID != nil {
				kind = "possible"
			}
			if _, err := s.pool.Exec(ctx, `
INSERT INTO review_items (source, status, email, first_name, last_name, nickname, phone, data, department_id, bundle_ids,
                          match_person_id, match_kind, import_batch_id, external_ref)
VALUES ('import', $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
				readyStatus(r), r.Email, r.FirstName, r.LastName, r.Nickname, r.Phone, raw, row.DepartmentID, bundles,
				row.MatchPersonID, kind, batchID, r.ExternalRef); err != nil {
				return nil, httperror.Internal(err)
			}
			created++
		}
	}
	errJSON, _ := json.Marshal(errRows)
	if _, err := s.pool.Exec(ctx, `
UPDATE import_batches SET rows_created = $2, rows_updated = $3, rows_failed = $4, errors = $5 WHERE id = $1`,
		batchID, created, updated, len(errRows), errJSON); err != nil {
		return nil, httperror.Internal(err)
	}
	if err := s.audit.Record(ctx, actor, "import.commit", "import_batch", batchID, map[string]any{
		"file": name, "rows": len(prev.Rows), "created": created, "updated": updated, "failed": len(errRows),
	}); err != nil {
		return nil, err
	}
	return s.Batch(ctx, batchID)
}

const batchCols = `b.id, b.file_name, b.created_by, b.created_at, b.rows_total, b.rows_created, b.rows_updated, b.rows_failed, b.errors,
b.undone_at, (SELECT count(*) FROM review_items r WHERE r.import_batch_id = b.id AND r.status IN ('pending', 'ready'))`

func scanBatch(row rowScanner) (*ImportBatch, error) {
	b := &ImportBatch{}
	var errs []byte
	if err := row.Scan(&b.ID, &b.FileName, &b.CreatedBy, &b.CreatedAt, &b.RowsTotal, &b.RowsCreated, &b.RowsUpdated, &b.RowsFailed,
		&errs, &b.UndoneAt, &b.Open); err != nil {
		return nil, err
	}
	_ = json.Unmarshal(errs, &b.Errors)
	return b, nil
}

// Batch returns one batch.
func (s *ImportService) Batch(ctx context.Context, id uuid.UUID) (*ImportBatch, error) {
	b, err := scanBatch(s.pool.QueryRow(ctx, `SELECT `+batchCols+` FROM import_batches b WHERE b.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, httperror.NotFound("import")
	}
	if err != nil {
		return nil, httperror.Internal(err)
	}
	return b, nil
}

// Batches lists imports, newest first (without their error rows).
func (s *ImportService) Batches(ctx context.Context) ([]ImportBatch, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+batchCols+` FROM import_batches b ORDER BY b.created_at DESC LIMIT 100`)
	if err != nil {
		return nil, httperror.Internal(err)
	}
	defer rows.Close()
	out := []ImportBatch{}
	for rows.Next() {
		b, err := scanBatch(rows)
		if err != nil {
			return nil, httperror.Internal(err)
		}
		b.Errors = nil
		out = append(out, *b)
	}
	return out, rows.Err()
}

// Undo removes a batch's items while none of them was accepted or merged.
func (s *ImportService) Undo(ctx context.Context, actor string, id uuid.UUID) (*ImportBatch, error) {
	b, err := s.Batch(ctx, id)
	if err != nil {
		return nil, err
	}
	if b.UndoneAt != nil {
		return nil, httperror.Conflict("this import was already undone")
	}
	var decided int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM review_items WHERE import_batch_id = $1 AND status IN ('accepted', 'merged')`, id).Scan(&decided); err != nil {
		return nil, httperror.Internal(err)
	}
	if decided > 0 {
		return nil, httperror.Conflict(strconv.Itoa(decided) + " people from this import were already accepted or merged, so it can no longer be undone")
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM review_items WHERE import_batch_id = $1`, id)
	if err != nil {
		return nil, httperror.Internal(err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE import_batches SET undone_at = now() WHERE id = $1`, id); err != nil {
		return nil, httperror.Internal(err)
	}
	if err := s.audit.Record(ctx, actor, "import.undo", "import_batch", id, map[string]any{"removed": tag.RowsAffected()}); err != nil {
		return nil, err
	}
	return s.Batch(ctx, id)
}

// ReportFromPreview builds the error report of a dry run.
func ReportFromPreview(p *PreviewResult) ([]byte, error) {
	cols := map[string]bool{}
	rows := []map[string]string{}
	for _, r := range p.Rows {
		if len(r.Problems) == 0 {
			continue
		}
		row := map[string]string{"row": strconv.Itoa(r.Row), "problem": strings.Join(r.Problems, "; ")}
		for k, v := range r.values {
			cols[k] = true
			row[k] = v
		}
		rows = append(rows, row)
	}
	return importer.ErrorReport(sortedKeys(cols), rows)
}

// ReportFromBatch builds the error report of a committed import.
func ReportFromBatch(b *ImportBatch) ([]byte, error) {
	cols := map[string]bool{}
	rows := []map[string]string{}
	for _, e := range b.Errors {
		row := map[string]string{"row": fmt.Sprint(e["row"]), "problem": fmt.Sprint(e["problem"])}
		if vals, ok := e["values"].(map[string]any); ok {
			for k, v := range vals {
				cols[k] = true
				row[k] = fmt.Sprint(v)
			}
		}
		rows = append(rows, row)
	}
	return importer.ErrorReport(sortedKeys(cols), rows)
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ImportTemplate is a saved mapping.
type ImportTemplate struct {
	ID        uuid.UUID     `json:"id"`
	Name      string        `json:"name"`
	Mapping   ImportOptions `json:"mapping"`
	UpdatedAt time.Time     `json:"updated_at"`
}

// Templates lists saved mappings.
func (s *ImportService) Templates(ctx context.Context) ([]ImportTemplate, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, name, mapping, updated_at FROM import_templates ORDER BY lower(name)`)
	if err != nil {
		return nil, httperror.Internal(err)
	}
	defer rows.Close()
	out := []ImportTemplate{}
	for rows.Next() {
		var t ImportTemplate
		var raw []byte
		if err := rows.Scan(&t.ID, &t.Name, &raw, &t.UpdatedAt); err != nil {
			return nil, httperror.Internal(err)
		}
		_ = json.Unmarshal(raw, &t.Mapping)
		out = append(out, t)
	}
	return out, rows.Err()
}

// SaveTemplate creates or replaces a mapping by name.
func (s *ImportService) SaveTemplate(ctx context.Context, actor, name string, m ImportOptions) (*ImportTemplate, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, httperror.Validation("validation failed", map[string]string{"name": "required"})
	}
	raw, _ := json.Marshal(m)
	t := &ImportTemplate{Name: name, Mapping: m}
	if err := s.pool.QueryRow(ctx, `
INSERT INTO import_templates (name, mapping) VALUES ($1, $2)
ON CONFLICT (lower(name)) DO UPDATE SET mapping = EXCLUDED.mapping, updated_at = now()
RETURNING id, updated_at`, name, raw).Scan(&t.ID, &t.UpdatedAt); err != nil {
		return nil, httperror.Internal(err)
	}
	_ = s.audit.Record(ctx, actor, "import.template.save", "import_template", t.ID, map[string]string{"name": name})
	return t, nil
}

// DeleteTemplate removes a mapping.
func (s *ImportService) DeleteTemplate(ctx context.Context, actor string, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM import_templates WHERE id = $1`, id)
	if err != nil {
		return httperror.Internal(err)
	}
	if tag.RowsAffected() == 0 {
		return httperror.NotFound("import template")
	}
	return s.audit.Record(ctx, actor, "import.template.delete", "import_template", id, nil)
}
