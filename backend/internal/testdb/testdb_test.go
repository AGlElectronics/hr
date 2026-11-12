package testdb

import (
	"context"
	"strings"
	"testing"
)

func TestSearchPathRejectsUnsafeNames(t *testing.T) {
	for _, name := range []string{"", "public", "hr_test_abc", "hr_test_" + strings.Repeat("a", 31), "hr_test_" + strings.Repeat("a", 32) + ";drop"} {
		if _, err := searchPath(name); err == nil {
			t.Fatalf("searchPath(%q) accepted an unsafe name", name)
		}
	}
	name := "hr_test_" + strings.Repeat("ab", 16)
	got, err := searchPath(name)
	if err != nil {
		t.Fatal(err)
	}
	if got != name {
		t.Fatalf("searchPath = %q", got)
	}
}

func TestOpenSchemaDoesNotWriteTheSharedDatabase(t *testing.T) {
	shared := Open(t)
	private := OpenSchema(t)
	ctx := context.Background()

	var schema string
	if err := private.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(schema, "hr_test_") {
		t.Fatalf("current_schema = %q", schema)
	}
	var before int
	if err := shared.QueryRow(ctx, `SELECT count(*) FROM den_link`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err := private.Exec(ctx, `
		INSERT INTO den_link (id, den_base_url, credential) VALUES (1, 'http://den.example', 'x')`); err != nil {
		t.Fatal(err)
	}
	var inSchema int
	if err := private.QueryRow(ctx, `SELECT count(*) FROM den_link`).Scan(&inSchema); err != nil {
		t.Fatal(err)
	}
	if inSchema != 1 {
		t.Fatalf("private den_link rows = %d", inSchema)
	}
	var after int
	if err := shared.QueryRow(ctx, `SELECT count(*) FROM den_link`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("shared den_link rows %d -> %d", before, after)
	}
}
