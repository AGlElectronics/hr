package http

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// healthHandler serves /livez and /readyz. Kubernetes-friendly:
//   - /livez returns 200 as long as the process is running
//   - /readyz returns 200 only when all critical downstreams respond
type healthHandler struct {
	pool      *pgxpool.Pool
	startedAt time.Time
	version   string
}

func newHealthHandler(pool *pgxpool.Pool, version string) *healthHandler {
	return &healthHandler{pool: pool, startedAt: time.Now(), version: version}
}

type livezResponse struct {
	Status    string `json:"status"`
	Version   string `json:"version"`
	UptimeSec int64  `json:"uptime_sec"`
}

func (h *healthHandler) livez(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(livezResponse{
		Status:    "ok",
		Version:   h.version,
		UptimeSec: int64(time.Since(h.startedAt).Seconds()),
	})
}

type readyzResponse struct {
	Status   string `json:"status"`
	Database string `json:"database"`
}

func (h *healthHandler) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	dbStatus := "ok"
	if h.pool == nil {
		dbStatus = "not_configured"
	} else if err := h.pool.Ping(ctx); err != nil {
		dbStatus = "down"
	}

	resp := readyzResponse{Status: "ready", Database: dbStatus}
	status := http.StatusOK
	if dbStatus != "ok" {
		resp.Status = "not_ready"
		status = http.StatusServiceUnavailable
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(resp)
}
