package pdfform_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/AGlElectronics/hr/backend/internal/pdfform"
)

func TestListFieldsLibreOfficeSample(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "OoPdfFormExample.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	fields, err := pdfform.ListFields(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(fields) < 10 {
		t.Fatalf("expected many AcroForm fields, got %d", len(fields))
	}
	names := map[string]bool{}
	for _, f := range fields {
		names[f.Name] = true
	}
	for _, want := range []string{"Given Name Text Box", "Family Name Text Box", "Country Combo Box"} {
		if !names[want] {
			t.Fatalf("missing field %q in %#v", want, fields)
		}
	}
}
