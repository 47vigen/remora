package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/47vigen/remora/internal/alias"
)

func openTemp(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "remora.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return s
}

func TestOpenAppliesMigrations(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)

	files, err := migrationFS.ReadDir("migrations")
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}
	var version int
	if err := s.DB().QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("user_version: %v", err)
	}
	// user_version counts the applied files, so a gap in the file numbers (0005 before 0004) is harmless.
	if version != len(files) || version != 8 {
		t.Errorf("user_version = %d, want %d (files) == 8", version, len(files))
	}

	for _, table := range []string{
		"settings", "engine_state", "providers", "provider_keys", "client_keys", "aliases", "alias_members", "engine_sync",
		"accounts", "sessions", "api_tokens", "account_strategies",
	} {
		var n int
		err := s.DB().QueryRowContext(ctx,
			"SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&n)
		if err != nil {
			t.Fatalf("sqlite_master: %v", err)
		}
		if n != 1 {
			t.Errorf("table %q missing", table)
		}
	}

	var mode string
	if err := s.DB().QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatalf("journal_mode: %v", err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q, want wal", mode)
	}
	var fk int
	if err := s.DB().QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatalf("foreign_keys: %v", err)
	}
	if fk != 1 {
		t.Errorf("foreign_keys = %d, want 1", fk)
	}
}

func TestMigrationRenamesStrategies(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "remora.db")
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	names, err := migrations()
	if err != nil {
		t.Fatalf("migrations: %v", err)
	}
	old := slices.Index(names, "0006_alias_strategy.sql") + 1
	for i, name := range names[:old] {
		body, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if _, err := db.ExecContext(ctx, string(body)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
		if _, err := db.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", i+1)); err != nil {
			t.Fatalf("user_version: %v", err)
		}
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO aliases (name, kind, strategy) VALUES ('a', 'chat', 'round-robin'), ('b', 'chat', '')`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	s, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	all, err := s.Aliases(ctx)
	if err != nil || all["a"].Strategy != alias.StrategyRandom || all["b"].Strategy != alias.StrategyPriority {
		t.Errorf("Aliases = (%+v, %v), want a random, b priority", all, err)
	}
	if v, err := userVersion(ctx, s.DB()); err != nil || v != len(names) {
		t.Errorf("user_version = (%d, %v), want %d", v, err, len(names))
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "remora.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.SetSetting(ctx, "k", "v"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	s, err = Open(ctx, path)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	v, ok, err := s.Setting(ctx, "k")
	if v != "v" || !ok || err != nil {
		t.Errorf("Setting(k) = (%q, %v, %v), want (v, true, nil)", v, ok, err)
	}
}

func TestOpenRejectsNewerSchema(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "remora.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := s.DB().ExecContext(ctx, "PRAGMA user_version = 99"); err != nil {
		t.Fatalf("set user_version: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	s, err = Open(ctx, path)
	if !errors.Is(err, ErrSchemaTooNew) {
		if s != nil {
			_ = s.Close()
		}
		t.Fatalf("Open = %v, want ErrSchemaTooNew", err)
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)

	v, ok, err := s.Setting(ctx, "missing")
	if v != "" || ok || err != nil {
		t.Errorf("Setting(missing) = (%q, %v, %v), want (\"\", false, nil)", v, ok, err)
	}
	for _, val := range []string{"1", "2"} {
		if err := s.SetSetting(ctx, "a", val); err != nil {
			t.Fatalf("SetSetting(a, %s): %v", val, err)
		}
	}
	v, ok, err = s.Setting(ctx, "a")
	if v != "2" || !ok || err != nil {
		t.Errorf("Setting(a) = (%q, %v, %v), want (2, true, nil)", v, ok, err)
	}
}

func TestEngineValueRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)

	v, ok, err := s.EngineValue(ctx, "bifrost", "x")
	if v != "" || ok || err != nil {
		t.Errorf("EngineValue(bifrost, x) = (%q, %v, %v), want (\"\", false, nil)", v, ok, err)
	}
	if err := s.SetEngineValue(ctx, "bifrost", "x", "1"); err != nil {
		t.Fatalf("SetEngineValue: %v", err)
	}
	if err := s.SetEngineValue(ctx, "cliproxyapi", "x", "2"); err != nil {
		t.Fatalf("SetEngineValue: %v", err)
	}

	tests := []struct{ engine, want string }{{"bifrost", "1"}, {"cliproxyapi", "2"}}
	for _, tt := range tests {
		v, ok, err := s.EngineValue(ctx, tt.engine, "x")
		if v != tt.want || !ok || err != nil {
			t.Errorf("EngineValue(%s, x) = (%q, %v, %v), want (%s, true, nil)", tt.engine, v, ok, err, tt.want)
		}
	}
}

func TestStorePoolKeepsConnections(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	if got := s.DB().Stats().MaxOpenConnections; got != maxConns {
		t.Fatalf("MaxOpenConnections = %d, want %d", got, maxConns)
	}
	// Hold maxConns connections at once, then give them back: all of them must stay open for the next requests.
	conns := make([]*sql.Conn, maxConns)
	for i := range conns {
		c, err := s.DB().Conn(ctx)
		if err != nil {
			t.Fatalf("Conn: %v", err)
		}
		conns[i] = c
	}
	for _, c := range conns {
		_ = c.Close()
	}
	if got := s.DB().Stats().Idle; got != maxConns {
		t.Errorf("idle connections = %d, want %d", got, maxConns)
	}
}

func TestStoreConcurrentReadsAndWrite(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for range 32 {
		wg.Go(func() {
			for range 20 {
				if _, err := s.Aliases(ctx); err != nil {
					errs <- err
					return
				}
			}
		})
	}
	err := s.Write(ctx, func(tx *Tx) error {
		return tx.PutAlias(ctx, alias.Alias{Name: "a", Kind: alias.KindChat, Members: []alias.Member{{Ref: "p/m"}}})
	})
	wg.Wait()
	close(errs)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	for err := range errs {
		t.Fatalf("read: %v", err)
	}
	got, err := s.Aliases(ctx)
	if err != nil || len(got) != 1 {
		t.Fatalf("Aliases = %v, %v; want one alias", got, err)
	}
}
