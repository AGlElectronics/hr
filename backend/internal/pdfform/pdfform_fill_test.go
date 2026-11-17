package pdfform_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/AGlElectronics/hr/backend/internal/pdfform"
)

func TestFillLibreOfficeSample(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "OoPdfFormExample.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := pdfform.Fill(b, map[string]string{
		"Given Name Text Box":  "Ada",
		"Family Name Text Box": "Lovelace",
		"City Text Box":        "Apeldoorn",
		"Postcode Text Box":    "7328 JL",
		"Country Combo Box":    "NL",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) < 1000 {
		t.Fatalf("filled pdf too small: %d", len(out))
	}
	// Re-list fields to ensure the PDF is still readable.
	fields, err := pdfform.ListFields(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(fields) < 10 {
		t.Fatalf("expected fields after fill, got %d", len(fields))
	}
}
