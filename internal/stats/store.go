// Package stats stores derived request statistics in minute buckets.
package stats

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // registers the SQLite driver
)

const (
	schemaVersion = 1
	replayWindow  = 15 * time.Minute
)

const schema = `
CREATE TABLE minute (
    bucket_unix_min INTEGER NOT NULL,
    provider TEXT NOT NULL,
    model TEXT NOT NULL,
    vk_id TEXT NOT NULL,
    rule_id TEXT NOT NULL,
    key_id TEXT NOT NULL,
    fallback INTEGER NOT NULL,
    requests INTEGER NOT NULL,
    errors INTEGER NOT NULL,
    cancelled INTEGER NOT NULL,
    prompt_tokens INTEGER NOT NULL,
    completion_tokens INTEGER NOT NULL,
    total_tokens INTEGER NOT NULL,
    cost_usd REAL NOT NULL,
    positive_cost_usd REAL NOT NULL,
    lat BLOB NOT NULL,
    PRIMARY KEY (bucket_unix_min, provider, model, vk_id, rule_id, key_id, fallback)
) WITHOUT ROWID;
CREATE TABLE seen (id TEXT PRIMARY KEY, ts INTEGER NOT NULL) WITHOUT ROWID;
CREATE INDEX seen_ts ON seen(ts);
CREATE TABLE cursor (name TEXT PRIMARY KEY, ts TEXT NOT NULL, id TEXT NOT NULL) WITHOUT ROWID;`

// Store holds the derived rollup, with a single SQLite connection serializing writes.
type Store struct {
	db *sql.DB
}

// Open opens the dedicated stats database at path. An existing database with a
// different schema version is discarded, so only one Store may own this path.
func Open(ctx context.Context, path string) (*Store, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	s, err := open(ctx, abs)
	if err != nil {
		return nil, fmt.Errorf("open request stats: %w", err)
	}
	return s, nil
}

func open(ctx context.Context, path string) (*Store, error) {
	_, err := os.Stat(path)
	fresh := errors.Is(err, os.ErrNotExist)
	if err != nil && !fresh {
		return nil, err
	}
	dsn := (&url.URL{Scheme: "file", Path: path, RawQuery: "_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=temp_store(MEMORY)&_txlock=immediate"}).String()
	connect := func() (*sql.DB, error) {
		db, err := sql.Open("sqlite", dsn)
		if err != nil {
			return nil, err
		}
		db.SetMaxOpenConns(1)
		return db, nil
	}
	db, err := connect()
	if err != nil {
		return nil, err
	}
	if !fresh {
		var version int
		if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
			_ = db.Close()
			return nil, err
		}
		if version == schemaVersion {
			return &Store{db: db}, nil
		}
		if err := db.Close(); err != nil {
			return nil, err
		}
		for _, suffix := range []string{"-shm", "-wal", ""} {
			if err := os.Remove(path + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
				return nil, err
			}
		}
		db, err = connect()
		if err != nil {
			return nil, err
		}
	}
	s := &Store{db: db}
	if err := s.write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, fmt.Sprintf("%s\nPRAGMA user_version = %d", schema, schemaVersion))
		return err
	}); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the SQLite connection.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) write(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// Cursor is the last point committed by a log feed. The zero value means it has not started.
type Cursor struct {
	Timestamp time.Time
	ID        string
}

// Cursor returns the named feed's persisted position, or the zero value if absent.
func (s *Store) Cursor(ctx context.Context, name string) (Cursor, error) {
	var c Cursor
	var ts string
	err := s.db.QueryRowContext(ctx, `SELECT ts, id FROM cursor WHERE name = ?`, name).Scan(&ts, &c.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return Cursor{}, nil
	}
	if err != nil {
		return Cursor{}, err
	}
	c.Timestamp, err = time.Parse(time.RFC3339Nano, ts)
	return c, err
}

// Prune removes expired minute buckets and replay IDs. The feed calls it hourly
// with the raw log retention; stats keep at least eight days.
func (s *Store) Prune(ctx context.Context, now time.Time, rawRetentionDays int) error {
	return s.prune(ctx, now, rawRetentionDays, now.Add(-replayWindow))
}

func (s *Store) prune(ctx context.Context, now time.Time, rawRetentionDays int, seenBefore time.Time) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		cutoff := now.UTC().AddDate(0, 0, -max(8, rawRetentionDays+1))
		if _, err := tx.ExecContext(ctx, `DELETE FROM minute WHERE bucket_unix_min < ?`, unixMinute(cutoff)); err != nil {
			return err
		}
		// Use insertion time, not request time: historical pages can overlap too.
		if seenBefore.Before(cutoff) {
			seenBefore = cutoff
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM seen WHERE ts < ?`, seenBefore.Unix())
		return err
	})
}
