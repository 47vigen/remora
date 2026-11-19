package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

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

// Tx is a store transaction. Store.Read supplies a read-only view.
type Tx struct {
	tx     *sql.Tx
	sealer *Sealer
}

// Read runs fn against one SQLite snapshot, without acquiring the write lock.
func (s *Store) Read(ctx context.Context, fn func(tx *Tx) error) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return fmt.Errorf("store: read: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	return fn(&Tx{tx: tx, sealer: s.sealer.Load()})
}

// Generation returns the reconcile-input generation in this transaction's snapshot.
func (tx *Tx) Generation(ctx context.Context) (int64, error) {
	var generation int64
	if err := tx.tx.QueryRowContext(ctx, "SELECT generation FROM store_generation").Scan(&generation); err != nil {
		return 0, fmt.Errorf("store: read generation: %w", err)
	}
	return generation, nil
}

// Write runs fn in one BEGIN IMMEDIATE transaction and commits iff fn returns nil, then refreshes the snapshot
// unless a reconcile write is pending. Concurrent writers serialise on the database lock; Write waits up to 30 s.
func (s *Store) Write(ctx context.Context, fn func(tx *Tx) error) error {
	if err := s.write(ctx, fn); err != nil {
		return err
	}
	// Refresh only after returning the write's connection to the pool.
	_ = s.Refresh(context.WithoutCancel(ctx))
	return nil
}

// PendingWrite holds a committed change whose publication waits for engine acceptance or compensation.
// The caller must resolve it once, through either Commit or Undo.
type PendingWrite struct {
	store *Store
	undo  []string
}

// WriteWithUndo commits fn without publishing it. The caller must resolve the returned PendingWrite.
func (s *Store) WriteWithUndo(ctx context.Context, fn func(tx *Tx) error) (*PendingWrite, error) {
	if err := s.beginPending(ctx); err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		if !committed {
			s.finishPending(ctx)
		}
	}()
	var undo []string
	tables := []string{"providers", "provider_keys", "aliases", "alias_members", "client_keys", "account_strategies", "engine_sync"}
	err := s.write(ctx, func(tx *Tx) error {
		// TEMP triggers see cascades too, but cannot capture writes on another connection.
		if _, err := tx.tx.ExecContext(ctx, "CREATE TEMP TABLE reconcile_undo (statement TEXT NOT NULL)"); err != nil {
			return err
		}
		for _, table := range tables {
			if err := tx.captureUndo(ctx, table); err != nil {
				return err
			}
		}
		if err := fn(tx); err != nil {
			return err
		}
		rows, err := tx.tx.QueryContext(ctx, "SELECT statement FROM reconcile_undo ORDER BY rowid DESC")
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var stmt string
			if err := rows.Scan(&stmt); err != nil {
				return err
			}
			undo = append(undo, stmt)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		// Dropping the journal alone would leave the TEMP triggers on the pooled connection.
		for _, table := range tables {
			for _, op := range []string{"insert", "update", "delete"} {
				if _, err := tx.tx.ExecContext(ctx, "DROP TRIGGER temp.reconcile_undo_"+table+"_"+op); err != nil {
					return err
				}
			}
		}
		_, err = tx.tx.ExecContext(ctx, "DROP TABLE temp.reconcile_undo")
		return err
	})
	if err != nil {
		return nil, err
	}
	committed = true
	return &PendingWrite{store: s, undo: undo}, nil
}

// Commit writes the engine's output and publishes the change. On error the change stays pending.
func (p *PendingWrite) Commit(ctx context.Context, fn func(tx *Tx) error) error {
	if err := p.store.write(ctx, fn); err != nil {
		return err
	}
	p.store.finishPending(ctx)
	return nil
}

// Undo compensates the write and publishes the restored state. On error the change stays pending.
func (p *PendingWrite) Undo(ctx context.Context) error {
	return p.Commit(ctx, func(tx *Tx) error {
		for _, stmt := range p.undo {
			if _, err := tx.tx.ExecContext(ctx, stmt); err != nil {
				return err
			}
		}
		return nil
	})
}

func (tx *Tx) captureUndo(ctx context.Context, table string) error {
	rows, err := tx.tx.QueryContext(ctx, "SELECT name FROM pragma_table_info(?, 'main') ORDER BY cid", table)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	literal := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
	value := func(ref string) string {
		// quote() truncates TEXT at a NUL. Encode text as bytes and cast back on replay.
		return fmt.Sprintf("CASE typeof(%[1]s) WHEN 'text' THEN 'CAST(X''' || hex(%[1]s) || ''' AS TEXT)' ELSE quote(%[1]s) END", ref)
	}
	var old, set, match []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		col := `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
		old = append(old, value("OLD."+col))
		set = append(set, literal(col+" = ")+" || "+value("OLD."+col))
		match = append(match, literal(col+" IS ")+" || "+value("NEW."+col))
	}
	if err := rows.Err(); err != nil {
		return err
	}
	// The postimage guard keeps a later independent writer's row authoritative.
	where := " || ' WHERE ' || " + strings.Join(match, " || ' AND ' || ")
	for _, trigger := range []struct{ op, stmt string }{
		{"insert", literal("DELETE FROM "+table) + where},
		{"update", literal("UPDATE "+table+" SET ") + " || " + strings.Join(set, " || ', ' || ") + where},
		{"delete", literal("INSERT INTO "+table+" VALUES (") + " || " + strings.Join(old, " || ', ' || ") + " || ') ON CONFLICT DO NOTHING'"},
	} {
		//nolint:gosec // G201: table and operation names are fixed, and column identifiers are quoted.
		ddl := fmt.Sprintf(`CREATE TEMP TRIGGER reconcile_undo_%[1]s_%[2]s AFTER %[2]s ON main.%[1]s
BEGIN INSERT INTO reconcile_undo VALUES (%[3]s); END`, table, trigger.op, trigger.stmt)
		if _, err := tx.tx.ExecContext(ctx, ddl); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) write(ctx context.Context, fn func(tx *Tx) error) (err error) {
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

type txKey struct{}

// WithTx returns ctx carrying tx, for callers that join an open write transaction instead of opening their own.
func WithTx(ctx context.Context, tx *Tx) context.Context { return context.WithValue(ctx, txKey{}, tx) }

// TxFrom returns the transaction ctx carries (see WithTx).
func TxFrom(ctx context.Context) (*Tx, bool) {
	tx, ok := ctx.Value(txKey{}).(*Tx)
	return tx, ok
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
