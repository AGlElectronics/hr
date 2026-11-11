package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func uploadFile(t *testing.T, router http.Handler, path, name, content string, opts any) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", name)
	_, _ = fw.Write([]byte(content))
	if opts != nil {
		raw, _ := json.Marshal(opts)
		_ = mw.WriteField("options", string(raw))
	}
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, path, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-Dev-User", "test-hr-admin")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// TestSpreadsheetImport: inspect, dry run with per-row results and an
// error report, commit into the review queue, re-run updates instead of
// duplicating, undo while nothing was accepted.
func TestSpreadsheetImport(t *testing.T) {
	router, pool, _, _ := fakeIdPRouter(t)
	admin := clientAs(router, "test-hr-admin", "")
	ctx := context.Background()
	dept, _ := createDepartment(t, admin, pool, "Security")
	tag := uniqueNick("imp")
	a, b := tag+"a@example.test", tag+"b@example.test"
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM review_items WHERE email LIKE $1`, tag+"%") })

	csv := "Vrijwilligers 2026\n" +
		"Voornaam;Achternaam;E-mail;Telefoon;Afdeling;Geboortedatum\n" +
		fmt.Sprintf("Anna;Alpha;%s;+44 20 7946 0000;Sec;10.12.1990\n", a) +
		fmt.Sprintf("Ben;Beta;mailto: %s ;442079460001;Sec;\n", strings.ToUpper(b)) +
		"Cleo;Gamma;;;;\n" +
		fmt.Sprintf("Anna;Again;%s;;;\n", a)

	rec := uploadFile(t, router, "/api/imports/inspect", "list.csv", csv, nil)
	var insp struct {
		Delimiter string            `json:"delimiter"`
		HeaderRow int               `json:"header_row"`
		Rows      int               `json:"rows"`
		Suggested map[string]string `json:"suggested_columns"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &insp)
	if rec.Code != http.StatusOK || insp.Delimiter != ";" || insp.HeaderRow != 2 || insp.Rows != 4 || insp.Suggested["email"] != "E-mail" ||
		insp.Suggested["department"] != "Afdeling" {
		t.Fatalf("inspect %d %s", rec.Code, rec.Body.String())
	}
	opts := map[string]any{"columns": insp.Suggested, "department_values": map[string]string{"sec": dept}}

	rec = uploadFile(t, router, "/api/imports/preview", "list.csv", csv, opts)
	var prev struct {
		Rows []struct {
			Row      int      `json:"row"`
			Status   string   `json:"status"`
			Email    string   `json:"email"`
			Problems []string `json:"problems"`
			Warnings []string `json:"warnings"`
		} `json:"rows"`
		Summary map[string]int `json:"summary"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &prev)
	if rec.Code != http.StatusOK || prev.Summary["new"] != 2 || prev.Summary["error"] != 1 || prev.Summary["duplicate"] != 1 {
		t.Fatalf("preview %d %s", rec.Code, rec.Body.String())
	}
	if ben := prev.Rows[1]; ben.Email != b || !strings.Contains(strings.Join(ben.Warnings, "|"), "cleaned") ||
		!strings.Contains(strings.Join(ben.Warnings, "|"), "stored as a number") {
		t.Fatalf("Excel damage warnings for row 4: %+v", ben)
	}
	var count int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM review_items WHERE email LIKE $1`, tag+"%").Scan(&count)
	if count != 0 {
		t.Fatal("the dry run wrote to the queue")
	}
	if rec = uploadFile(t, router, "/api/imports/preview?report=xlsx", "list.csv", csv, opts); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Header().Get("Content-Type"), "spreadsheetml") {
		t.Fatalf("error report %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}

	rec = uploadFile(t, router, "/api/imports", "list.csv", csv, opts)
	var batch struct {
		ID          string `json:"id"`
		RowsCreated int    `json:"rows_created"`
		RowsFailed  int    `json:"rows_failed"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &batch)
	if rec.Code != http.StatusCreated || batch.RowsCreated != 2 || batch.RowsFailed != 2 {
		t.Fatalf("commit %d %s", rec.Code, rec.Body.String())
	}
	items := reviewItems(t, admin, "import_batch_id="+batch.ID)
	if len(items) != 2 || items[0].Source != "import" {
		t.Fatalf("imported items: %+v", items)
	}
	var deptOK bool
	_ = pool.QueryRow(ctx, `SELECT bool_and(department_id = $1) FROM review_items WHERE import_batch_id = $2`, dept, batch.ID).Scan(&deptOK)
	if !deptOK {
		t.Fatal("department values not mapped")
	}
	if rec = admin(http.MethodGet, "/api/imports/"+batch.ID+"/errors", nil); rec.Code != http.StatusOK {
		t.Fatalf("batch error report %d", rec.Code)
	}

	rerun := strings.Replace(csv, "+44 20 7946 0000", "+44 20 7946 0999", 1)
	rec = uploadFile(t, router, "/api/imports", "list.csv", rerun, opts)
	var again struct {
		ID          string `json:"id"`
		RowsCreated int    `json:"rows_created"`
		RowsUpdated int    `json:"rows_updated"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &again)
	if again.RowsCreated != 0 || again.RowsUpdated != 1 {
		t.Fatalf("re-run should update, not duplicate: %s", rec.Body.String())
	}
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM review_items WHERE email LIKE $1`, tag+"%").Scan(&count)
	if count != 2 {
		t.Fatalf("items after re-run: %d", count)
	}

	if rec = admin(http.MethodDelete, "/api/imports/"+batch.ID, nil); rec.Code != http.StatusOK {
		t.Fatalf("undo %d %s", rec.Code, rec.Body.String())
	}
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM review_items WHERE email LIKE $1`, tag+"%").Scan(&count)
	if count != 0 {
		t.Fatalf("undo left %d items", count)
	}

	rec = uploadFile(t, router, "/api/imports", "list.csv", csv, opts)
	_ = json.Unmarshal(rec.Body.Bytes(), &batch)
	if _, err := pool.Exec(ctx, `UPDATE review_items SET status = 'accepted' WHERE import_batch_id = $1 AND email = $2`, batch.ID, a); err != nil {
		t.Fatal(err)
	}
	if rec = admin(http.MethodDelete, "/api/imports/"+batch.ID, nil); rec.Code != http.StatusConflict {
		t.Fatalf("undo after an accept: want 409, got %d", rec.Code)
	}
	lead := clientAs(router, "someone", "hr-lead")
	if rec = lead(http.MethodGet, "/api/imports", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("only HR admins import: got %d", rec.Code)
	}
}
