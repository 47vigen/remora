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
	"sort"

	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver
)

// ErrSchemaTooNew means the database was written by a newer remora. Open never migrates down.
var ErrSchemaTooNew = errors.New("store: database schema is newer than this remora")

//go:embed migrations/*.sql
var migrationFS embed.FS

// Store is the SQLite store.
type Store struct{ db *sql.DB }

// Open opens (or creates) the database at path and applies the embedded migrations that are missing.
func Open(ctx context.Context, path string) (*Store, error) {
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	s := &Store{db: db}
	if err := s.migrate(ctx); err != nil {
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

// dsn sets the pragmas on every pooled connection and makes every transaction BEGIN IMMEDIATE.
func dsn(p string) string {
	return "file:" + p + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_txlock=immediate"
}

// migrations returns the embedded migration file names, sorted.
func migrations() ([]string, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names, nil
}

// migrate applies the missing migrations. user_version counts the applied files.
func (s *Store) migrate(ctx context.Context) error {
	names, err := migrations()
	if err != nil {
		return err
	}
	current, err := userVersion(ctx, s.db)
	if err != nil {
		return err
	}
	if current > len(names) {
		return fmt.Errorf("%w: schema version %d, this remora knows %d", ErrSchemaTooNew, current, len(names))
	}
	for i := current; i < len(names); i++ {
		if err := s.apply(ctx, i+1, names[i]); err != nil {
			return err
		}
	}
	return nil
}

// apply runs one migration file and sets user_version to version in one transaction. It re-reads
// user_version inside the transaction so that two processes opening the same file never apply a file twice.
func (s *Store) apply(ctx context.Context, version int, name string) (err error) {
	body, err := migrationFS.ReadFile(path.Join("migrations", name))
	if err != nil {
		return fmt.Errorf("read migration %s: %w", name, err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("migration %s: begin: %w", name, err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	current, err := userVersion(ctx, tx)
	if err != nil {
		return err
	}
	if current >= version {
		return tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, string(body)); err != nil {
		return fmt.Errorf("migration %s: %w", name, err)
	}
	// PRAGMA takes no bound parameters; version is an int we computed.
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", version)); err != nil {
		return fmt.Errorf("migration %s: set user_version: %w", name, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("migration %s: commit: %w", name, err)
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
