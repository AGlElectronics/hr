package service

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
)

// maintenanceLockKey guards the maintenance run so only one replica does it.
const maintenanceLockKey int64 = 0x4852_0007

// Maintenance runs HR's periodic housekeeping: recover onboardings stuck in
// provisioning, purge expired access history, apply the retention rules
// and clean up Google Workspace accounts of offboarded people.
type Maintenance struct {
	pool   *pgxpool.Pool
	logger zerolog.Logger
	tasks  []maintenanceTask
}

type maintenanceTask struct {
	name string
	run  func(ctx context.Context) error
}

// NewMaintenance wires an empty Maintenance; Add registers tasks.
func NewMaintenance(pool *pgxpool.Pool, logger zerolog.Logger) *Maintenance {
	return &Maintenance{pool: pool, logger: logger.With().Str("component", "maintenance").Logger()}
}

// Add registers a task (run in the order added).
func (m *Maintenance) Add(name string, run func(ctx context.Context) error) {
	m.tasks = append(m.tasks, maintenanceTask{name: name, run: run})
}

// Run runs every task each interval until ctx ends.
func (m *Maintenance) Run(ctx context.Context, interval time.Duration) {
	if m == nil {
		return
	}
	if interval <= 0 {
		interval = time.Hour
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		m.RunOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// RunOnce runs every task once, unless another replica is already running
// them. A failing task is logged and does not stop the others.
func (m *Maintenance) RunOnce(ctx context.Context) {
	conn, err := m.pool.Acquire(ctx)
	if err != nil {
		m.logger.Warn().Err(err).Msg("maintenance: no database connection")
		return
	}
	defer conn.Release()
	var locked bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, maintenanceLockKey).Scan(&locked); err != nil || !locked {
		return
	}
	defer func() { _, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, maintenanceLockKey) }()
	for _, t := range m.tasks {
		func() {
			defer func() {
				if r := recover(); r != nil {
					m.logger.Error().Interface("panic", r).Str("task", t.name).Msg("maintenance task panicked")
				}
			}()
			if err := t.run(ctx); err != nil && ctx.Err() == nil {
				m.logger.Warn().Err(err).Str("task", t.name).Msg("maintenance task failed")
			}
		}()
	}
}
