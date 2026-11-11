package http

import (
	"net/http"
	"strconv"

	"github.com/google/uuid"

	"github.com/AGlElectronics/hr/backend/internal/authz"
	"github.com/AGlElectronics/hr/backend/internal/httperror"
)

// ok renders err and reports false when it is non-nil.
func ok(w http.ResponseWriter, r *http.Request, err error) bool {
	if err != nil {
		httperror.Render(w, r, err)
		return false
	}
	return true
}

// scope returns nil for callers who read everything, or the departments a
// lead may see.
func scope(w http.ResponseWriter, r *http.Request, p authz.Permission) ([]uuid.UUID, bool) {
	s, err := authz.ScopeFilter(r.Context(), p)
	return s, ok(w, r, err)
}

func queryInt(r *http.Request, name string) int {
	n, _ := strconv.Atoi(r.URL.Query().Get(name))
	return n
}

func queryUUID(r *http.Request, name string) (uuid.UUID, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return uuid.Nil, nil
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, httperror.BadRequest(name + " must be a uuid")
	}
	return id, nil
}
