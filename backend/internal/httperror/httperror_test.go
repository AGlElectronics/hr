package httperror

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRender_TypedError(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)

	Render(rec, req, Validation("bad field", map[string]any{"field": "name"}))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("got %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}
	var body struct {
		Error Error `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Error.Code != CodeValidation {
		t.Fatalf("code=%q want %q", body.Error.Code, CodeValidation)
	}
}

func TestRender_UnknownErrorBecomes500(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)

	Render(rec, req, errors.New("boom"))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("got %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestNotFound_Helper(t *testing.T) {
	e := NotFound("widget")
	if e.Code != CodeNotFound {
		t.Fatalf("code=%q", e.Code)
	}
	if e.Message != "widget not found" {
		t.Fatalf("msg=%q", e.Message)
	}
}
