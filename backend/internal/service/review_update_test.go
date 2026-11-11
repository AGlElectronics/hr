package service

import (
	"encoding/json"
	"testing"
)

// A review update that leaves out department_id or bundle_ids keeps them;
// an explicit null department clears it.
func TestReviewUpdateInputPresence(t *testing.T) {
	var notesOnly ReviewUpdateInput
	if err := json.Unmarshal([]byte(`{"notes":"called them"}`), &notesOnly); err != nil {
		t.Fatal(err)
	}
	if notesOnly.hasDepartment || notesOnly.hasBundles || notesOnly.Notes == nil {
		t.Fatalf("notes only: %+v", notesOnly)
	}

	var clear ReviewUpdateInput
	if err := json.Unmarshal([]byte(`{"department_id":null,"bundle_ids":[]}`), &clear); err != nil {
		t.Fatal(err)
	}
	if !clear.hasDepartment || !clear.hasBundles || clear.DepartmentID != nil {
		t.Fatalf("explicit clear: %+v", clear)
	}
}
