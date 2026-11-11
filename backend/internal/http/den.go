package http

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/AGlElectronics/hr/backend/internal/audit"
)

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// audited records action in the audit log after h answered with a 2xx
// (the Den pairing routes, which live in the shared denlink package).
func audited(aw *audit.Writer, action string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		h(rec, r)
		if rec.status < 300 {
			_ = aw.Record(r.Context(), actorFrom(r), action, "den_link", uuid.Nil, nil)
		} else {
			_ = aw.Record(r.Context(), actorFrom(r), action+".failed", "den_link", uuid.Nil, map[string]int{"status": rec.status})
		}
	}
}
