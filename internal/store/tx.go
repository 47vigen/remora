package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// Repository errors.
var (
	ErrNotFound  = errors.New("store: not found")
	ErrDuplicate = errors.New("store: duplicate")
)

// writeBusyTimeoutMS is how long Write waits for the database lock held by another writer (another process
// running apply, or the server's reconcile timer).
const writeBusyTimeoutMS = 30000

// Tx is a write transaction opened by Store.Write.
type Tx struct {
	tx     *sql.Tx
	sealer *Sealer
}

// Write runs fn in one BEGIN IMMEDIATE transaction and commits iff fn returns nil. Writers in other processes
// serialise on the database lock; Write waits up to 30 s for it.
func (s *Store) Write(ctx context.Context, fn func(tx *Tx) error) (err error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("store: write: conn: %w", err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(ctx, fmt.Sprintf("PRAGMA busy_timeout = %d", writeBusyTimeoutMS)); err != nil {
		return fmt.Errorf("store: write: busy timeout: %w", err)
	}
	// Give the pooled connection back with the default timeout, even when ctx is done.
	defer func() {
		_, _ = conn.ExecContext(context.WithoutCancel(ctx), fmt.Sprintf("PRAGMA busy_timeout = %d", busyTimeoutMS))
	}()

	sqlTx, err := conn.BeginTx(ctx, nil) // BEGIN IMMEDIATE: the DSN sets _txlock=immediate
	if err != nil {
		return fmt.Errorf("store: write: begin: %w", err)
	}
	defer func() { _ = sqlTx.Rollback() }() // no-op after Commit; covers a panic in fn

	if err := fn(&Tx{tx: sqlTx, sealer: s.sealer.Load()}); err != nil {
		return fmt.Errorf("store: write: %w", err)
	}
	if err := sqlTx.Commit(); err != nil {
		return fmt.Errorf("store: write: commit: %w", err)
	}
	return nil
}

// dbtx is what both *sql.DB and *sql.Tx offer, so read helpers serve Store and Tx alike.
type dbtx interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// mapErr turns a SQLite unique or primary-key violation into ErrDuplicate.
func mapErr(err error) error {
	var se *sqlite.Error
	if errors.As(err, &se) {
		switch se.Code() {
		case sqlite3.SQLITE_CONSTRAINT_UNIQUE, sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY:
			return fmt.Errorf("%w: %w", ErrDuplicate, err)
		}
	}
	return err
}

// mustAffect returns ErrNotFound when res changed no row.
func mustAffect(res sql.Result, what string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: %s: %w", what, err)
	}
	if n == 0 {
		return fmt.Errorf("%w: %s", ErrNotFound, what)
	}
	return nil
}

func encodeList(l []string) string {
	if len(l) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(l) // a []string always marshals
	return string(b)
}

// decodeList decodes a JSON array column; an empty array decodes to nil.
func decodeList(s string) ([]string, error) {
	var l []string
	if err := json.Unmarshal([]byte(s), &l); err != nil {
		return nil, fmt.Errorf("decode list %q: %w", s, err)
	}
	if len(l) == 0 {
		return nil, nil
	}
	return l, nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// newID returns a random UUID v4.
func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:]) // crypto/rand.Read never fails (it aborts the program instead)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// GenerateKeyValue returns a new client key value: "sk-" + base64url(32 random bytes), no padding.
func GenerateKeyValue() string {
	var b [32]byte
	_, _ = rand.Read(b[:]) // crypto/rand.Read never fails (it aborts the program instead)
	return "sk-" + base64.RawURLEncoding.EncodeToString(b[:])
}
