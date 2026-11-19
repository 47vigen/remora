// Package store is remora's SQLite store: schema, migrations and repositories.
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver
)

// ErrSchemaTooNew means the database was written by a newer remora. Open never migrates down.
var ErrSchemaTooNew = errors.New("store: database schema is newer than this remora")

//go:embed migrations/*.sql
var migrationFS embed.FS

// Store is the SQLite store.
type Store struct {
	db     *sql.DB
	sealer atomic.Pointer[Sealer] // nil until SetSealer; repositories that touch secrets need it

	snap    atomic.Pointer[snapshot] // nil until the first Refresh, or after one failed: reads use SQL
	snapMu  sync.Mutex               // serialises publication and SQL fallback reads
	pending bool                     // guarded by snapMu; a reconcile write has not resolved
}

// Open opens (or creates) the database at path and applies the embedded migrations that are missing.
func Open(ctx context.Context, path string) (*Store, error) {
	return openWithMigrations(ctx, path, migrationFS)
}

func openWithMigrations(ctx context.Context, path string, files fs.FS) (*Store, error) {
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	// Every /v1 request reads the store; with database/sql's default of 2 idle connections, any concurrency beyond
	// that opens a fresh SQLite connection (and re-reads the schema) per request.
	db.SetMaxOpenConns(maxConns)
	db.SetMaxIdleConns(maxConns)
	db.SetConnMaxIdleTime(0)
	s := &Store{db: db}
	if err := s.migrate(ctx, files); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error {
	return s.db.Close()
}

// DB returns the underlying database, for tests and tools.
func (s *Store) DB() *sql.DB {
	return s.db
}

// maxConns bounds the pool and is also how many connections stay open between requests.
const maxConns = 16

// busyTimeoutMS is how long a statement waits for the database lock. Write waits longer (writeBusyTimeoutMS).
const busyTimeoutMS = 5000

// dsn sets the pragmas on every pooled connection and makes every transaction BEGIN IMMEDIATE.
func dsn(p string) string {
	return "file:" + p + "?_pragma=busy_timeout(" + strconv.Itoa(busyTimeoutMS) + ")&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_txlock=immediate"
}

const numberedApplicationID = 0x524d5241 // RMRA

// Legacy releases stored positions in this list, not the prefixes. Never extend or reorder it.
var legacyMigrations = [...]string{
	"0001_init.sql",
	"0002_inference_core.sql",
	"0003_accounts.sql",
	"0005_auth.sql",
	"0006_alias_strategy.sql",
	"0007_account_quota.sql",
	"0008_alias_strategy_names.sql",
	"0009_account_routing.sql",
}

type migration struct {
	name    string
	version int
}

func migrations(files fs.FS) ([]migration, error) {
	entries, err := fs.ReadDir(files, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}
	list := make([]migration, 0, len(entries))
	var previous uint64
	for _, entry := range entries {
		name := entry.Name()
		prefix, _, ok := strings.Cut(name, "_")
		v, err := strconv.ParseUint(prefix, 10, 31) // SQLite user_version is a signed 32-bit integer.
		if entry.IsDir() || !ok || err != nil || v <= previous {
			return nil, fmt.Errorf("migration %s: expected a positive, strictly increasing numeric prefix", name)
		}
		list = append(list, migration{name: name, version: int(v)})
		previous = v
	}
	return list, nil
}

func (s *Store) migrate(ctx context.Context, files fs.FS) error {
	list, err := migrations(files)
	if err != nil {
		return err
	}
	// BEGIN IMMEDIATE serializes history validation and schema changes across concurrent opens.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migrations: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	current, err := userVersion(ctx, tx)
	if err != nil {
		return err
	}
	var id int
	if err := tx.QueryRowContext(ctx, "PRAGMA application_id").Scan(&id); err != nil {
		return fmt.Errorf("read application_id: %w", err)
	}
	latest := 0
	if len(list) != 0 {
		latest = list[len(list)-1].version
	}
	if id != numberedApplicationID {
		latest = min(len(legacyMigrations), len(list))
	}
	if current > latest {
		return fmt.Errorf("%w: schema version %d, this remora knows %d", ErrSchemaTooNew, current, latest)
	}
	if current < 0 {
		return fmt.Errorf("invalid schema version %d", current)
	}

	applied := 0
	if id != numberedApplicationID {
		applied = current
		if err := convertLegacyMigrations(ctx, tx, list[:applied]); err != nil {
			return err
		}
	} else {
		for applied < len(list) && list[applied].version <= current {
			applied++
		}
		if current != 0 && (applied == 0 || list[applied-1].version != current) {
			return fmt.Errorf("no migration for schema version %d", current)
		}
		if err := checkMigrationHistory(ctx, tx, list[:applied]); err != nil {
			return err
		}
	}
	for _, m := range list[applied:] {
		body, err := fs.ReadFile(files, path.Join("migrations", m.name))
		if err != nil {
			return fmt.Errorf("read migration %s: %w", m.name, err)
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			return fmt.Errorf("migration %s: %w", m.name, err)
		}
		if err := recordMigration(ctx, tx, m); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migrations: %w", err)
	}
	return nil
}

func convertLegacyMigrations(ctx context.Context, tx *sql.Tx, applied []migration) error {
	if _, err := tx.ExecContext(ctx, "CREATE TABLE schema_migrations (name TEXT PRIMARY KEY NOT NULL)"); err != nil {
		return fmt.Errorf("create migration history: %w", err)
	}
	for i, m := range applied {
		if m.name != legacyMigrations[i] {
			return fmt.Errorf("legacy migration history: expected %s, found %s", legacyMigrations[i], m.name)
		}
		if err := recordMigration(ctx, tx, m); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA application_id = %d", numberedApplicationID)); err != nil {
		return fmt.Errorf("set application_id: %w", err)
	}
	return nil
}

// user_version alone cannot tell whether a migration was inserted into an already-applied gap.
func checkMigrationHistory(ctx context.Context, tx *sql.Tx, applied []migration) error {
	rows, err := tx.QueryContext(ctx, "SELECT name FROM schema_migrations ORDER BY name")
	if err != nil {
		return fmt.Errorf("read migration history: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var i int
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return fmt.Errorf("read migration history: %w", err)
		}
		if i >= len(applied) || applied[i].name != name {
			return fmt.Errorf("migration history differs at %s", name)
		}
		i++
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read migration history: %w", err)
	}
	if i != len(applied) {
		return fmt.Errorf("migration history is missing %s", applied[i].name)
	}
	return nil
}

func recordMigration(ctx context.Context, tx *sql.Tx, m migration) error {
	if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations (name) VALUES (?)", m.name); err != nil {
		return fmt.Errorf("record migration %s: %w", m.name, err)
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", m.version)); err != nil {
		return fmt.Errorf("migration %s: set user_version: %w", m.name, err)
	}
	return nil
}

type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func userVersion(ctx context.Context, q queryer) (int, error) {
	var v int
	if err := q.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
		return 0, fmt.Errorf("read user_version: %w", err)
	}
	return v, nil
}
