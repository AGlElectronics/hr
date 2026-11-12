// Package testdb opens the database shared by HR's tests and applies Goose
// migrations once per process. A fresh Postgres has no tables until that
// happens, so every database test must call Open or OpenSchema itself.
package testdb

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/AGlElectronics/hr/backend/migrations"
)

// databaseEnv is the Postgres URL set by CI (the module pipeline's
// test-database-env input) and by local runs.
const databaseEnv = "HR_TEST_DATABASE_URL"

// migrateLockKey keeps two test processes from applying Goose at once. It is distinct
// from the maintenance and role-gate locks, so a test does not skip or
// block those workers when they share a database.
const migrateLockKey int64 = 0x4852_0071

// schemaName is the only shape OpenSchema puts in search_path.
var schemaName = regexp.MustCompile(`^hr_test_[0-9a-f]{32}$`)

var (
	migrateMu sync.Mutex
	migrated  = map[string]struct{}{}
)

// Open returns a pool for HR_TEST_DATABASE_URL after applying migrations.
// Migrations run once in this process. Tests share the database: they must
// remove the rows they insert and must not assume another test has run.
func Open(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return connect(t, requireDSN(t), "")
}

// OpenSchema returns a pool whose search_path is a new schema. The schema
// is migrated and dropped with the test. Use it when a test reads or writes
// every row of a table, or stores a singleton such as den_link, so it cannot
// see or change another test's data.
func OpenSchema(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := requireDSN(t)
	// Background, not a timeout context: cancelling the context passed to
	// pgxpool.New does not close the pool, but a short parent context makes
	// the later DROP SCHEMA easy to get wrong. Exec calls carry the timeout.
	admin, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("database: %v", err)
	}
	schema, err := newSchemaName()
	if err != nil {
		admin.Close()
		t.Fatal(err)
	}
	quoted := pgx.Identifier{schema}.Sanitize()
	createCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := admin.Exec(createCtx, `CREATE SCHEMA `+quoted); err != nil {
		admin.Close()
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer ccancel()
		_, dropErr := admin.Exec(cctx, `DROP SCHEMA `+quoted+` CASCADE`)
		admin.Close()
		if dropErr != nil {
			t.Errorf("drop schema %s: %v", schema, dropErr)
		}
	})
	return connect(t, dsn, schema)
}

func requireDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv(databaseEnv)
	if dsn == "" {
		t.Skip(databaseEnv + " not set")
	}
	return dsn
}

func newSchemaName() (string, error) {
	name := "hr_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if !schemaName.MatchString(name) {
		return "", fmt.Errorf("generated schema name %q is not safe", name)
	}
	return name, nil
}

func connect(t *testing.T, dsn, schema string) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := migrate(ctx, dsn, schema); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// The pool outlives this function. Give it a context that is not
	// cancelled when the migration timeout ends.
	pool, err := newPool(context.Background(), dsn, schema)
	if err != nil {
		t.Fatalf("database: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func newPool(ctx context.Context, dsn, schema string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse database URL: %w", err)
	}
	if err := setSearchPath(cfg.ConnConfig, schema); err != nil {
		return nil, err
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect to database: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}

// migrate applies pending migrations to dsn (and schema, when set). A
// successful call is remembered for this process. An advisory lock keeps a
// second test process from migrating the same database at the same time.
func migrate(ctx context.Context, dsn, schema string) error {
	migrateMu.Lock()
	defer migrateMu.Unlock()
	key := dsn + "\n" + schema
	if _, ok := migrated[key]; ok {
		return nil
	}
	if err := apply(ctx, dsn, schema); err != nil {
		return err
	}
	migrated[key] = struct{}{}
	return nil
}

func apply(ctx context.Context, dsn, schema string) error {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return fmt.Errorf("parse database URL: %w", err)
	}
	if err := setSearchPath(cfg, schema); err != nil {
		return err
	}
	// The lock connection stays checked out for the whole migration, so the
	// pool must be able to open another connection for Goose.
	db := stdlib.OpenDB(*cfg)
	db.SetMaxOpenConns(4)
	defer func() { _ = db.Close() }()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("database connection: %w", err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, migrateLockKey); err != nil {
		return fmt.Errorf("lock migrations: %w", err)
	}
	defer func() {
		_, _ = conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, migrateLockKey)
	}()

	// Install pgcrypto in public before Goose runs. The migration's own
	// CREATE EXTENSION has no schema, so on a private search_path it would
	// put the extension's objects in that schema.
	if _, err := db.ExecContext(ctx, `CREATE EXTENSION IF NOT EXISTS pgcrypto WITH SCHEMA public`); err != nil {
		return fmt.Errorf("pgcrypto: %w", err)
	}

	goose.SetBaseFS(migrations.FS)
	goose.SetTableName("goose_db_version")
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("set goose dialect: %w", err)
	}
	if err := goose.UpContext(ctx, db, "."); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}

func setSearchPath(cfg *pgx.ConnConfig, schema string) error {
	if schema == "" {
		return nil
	}
	path, err := searchPath(schema)
	if err != nil {
		return err
	}
	if cfg.RuntimeParams == nil {
		cfg.RuntimeParams = map[string]string{}
	}
	cfg.RuntimeParams["search_path"] = path
	return nil
}

func searchPath(schema string) (string, error) {
	if !schemaName.MatchString(schema) {
		return "", fmt.Errorf("invalid test schema %q", schema)
	}
	// Public stays off the path. Goose looks up goose_db_version through
	// search_path, and a shared public.goose_db_version would make it skip
	// every migration. gen_random_uuid() is in pg_catalog, so it still
	// resolves. pgcrypto itself is installed in public by apply.
	return schema, nil
}
