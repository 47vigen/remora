package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

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

func TestMigrationsApplyWithGap(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)

	checkNumberedSchema(t, s, latestMigration(t))

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

func latestMigration(t *testing.T) int {
	t.Helper()
	files, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	var latest int
	if _, err := fmt.Sscanf(files[len(files)-1].Name(), "%d_", &latest); err != nil {
		t.Fatal(err)
	}
	return latest
}

func checkNumberedSchema(t *testing.T, s *Store, want int) {
	t.Helper()
	ctx := t.Context()
	if v, err := userVersion(ctx, s.DB()); err != nil || v != want {
		t.Errorf("user_version = (%d, %v), want %d", v, err, want)
	}
	var id int
	if err := s.DB().QueryRowContext(ctx, "PRAGMA application_id").Scan(&id); err != nil || id != 0x524d5241 {
		t.Errorf("application_id = (%#x, %v), want 0x524d5241", id, err)
	}
}

func legacyDB(t *testing.T, path string, count int) *sql.DB {
	t.Helper()
	ctx := t.Context()
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	files, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files[:count] {
		body, err := migrationFS.ReadFile("migrations/" + file.Name())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, string(body)); err != nil {
			t.Fatalf("apply %s: %v", file.Name(), err)
		}
	}
	if _, err := db.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", count)); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestMigrationsConvertLegacy(t *testing.T) {
	for _, count := range []int{8, 3} {
		t.Run(fmt.Sprintf("count_%d", count), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "remora.db")
			if err := legacyDB(t, path, count).Close(); err != nil {
				t.Fatal(err)
			}
			// Every existing migration creates or alters schema; replaying one fails Open.
			for range 2 {
				s, err := Open(t.Context(), path)
				if err != nil {
					t.Fatal(err)
				}
				checkNumberedSchema(t, s, latestMigration(t))
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestMigrationsRejectInvalidPrefixes(t *testing.T) {
	for _, name := range []string{"0001_duplicate.sql", "bad_prefix.sql", "0000_zero.sql", "10_out_of_order.sql"} {
		t.Run(name, func(t *testing.T) {
			files := fstest.MapFS{
				"migrations/0001_init.sql":  {Data: []byte("CREATE TABLE first (id INTEGER)")},
				"migrations/0011_later.sql": {Data: []byte("CREATE TABLE later (id INTEGER)")},
				"migrations/" + name:        {Data: []byte("CREATE TABLE second (id INTEGER)")},
			}
			s, err := openWithMigrations(t.Context(), filepath.Join(t.TempDir(), "remora.db"), files)
			if err == nil {
				_ = s.Close()
				t.Fatal("Open accepted invalid migration numbers")
			}
		})
	}
}

func TestMigrationsRejectInsertedBelowVersion(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "remora.db")
	files := fstest.MapFS{
		"migrations/0001_init.sql": {Data: []byte("CREATE TABLE first (id INTEGER)")},
		"migrations/0003_next.sql": {Data: []byte("CREATE TABLE third (id INTEGER)")},
	}
	s, err := openWithMigrations(ctx, path, files)
	if err != nil {
		t.Fatal(err)
	}
	checkNumberedSchema(t, s, 3)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	files["migrations/0002_inserted.sql"] = &fstest.MapFile{Data: []byte("CREATE TABLE second (id INTEGER)")}
	s, err = openWithMigrations(ctx, path, files)
	if err == nil {
		_ = s.Close()
		t.Fatal("Open accepted a migration inserted below the stored version")
	}
}

func TestMigrationsRenameStrategies(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "remora.db")
	// The fifth legacy file was 0006_alias_strategy.sql.
	db := legacyDB(t, path, 5)
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
	checkNumberedSchema(t, s, latestMigration(t))
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

func TestMigrationsRejectNewerSchema(t *testing.T) {
	for _, tc := range []struct {
		name    string
		version int
		legacy  bool
	}{
		{"numbered", 99, false},
		{"legacy", 9, true}, // 9 is a known prefix, but not a known legacy count.
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			path := filepath.Join(t.TempDir(), "remora.db")
			var db *sql.DB
			if tc.legacy {
				db = legacyDB(t, path, 8)
			} else {
				s, err := Open(ctx, path)
				if err != nil {
					t.Fatal(err)
				}
				db = s.DB()
			}
			t.Cleanup(func() { _ = db.Close() })
			if _, err := db.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", tc.version)); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			s, err := Open(ctx, path)
			if !errors.Is(err, ErrSchemaTooNew) {
				if s != nil {
					_ = s.Close()
				}
				t.Fatalf("Open = %v, want ErrSchemaTooNew", err)
			}
		})
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
	s := openSealed(t)

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

func TestEngineValueSealed(t *testing.T) {
	const value = "synthetic-engine-value"
	s := openSealed(t)
	if err := s.SetEngineValue(t.Context(), "test-engine", "api_key", value); err != nil {
		t.Fatal(err)
	}
	assertEngineValueSealed(t, s, value)
}

func TestEngineValueUpgradesPlaintext(t *testing.T) {
	const value = "synthetic-legacy-value"
	s := openSealed(t)
	if _, err := s.db.ExecContext(t.Context(), "INSERT INTO engine_state (engine, key, value) VALUES ('test-engine', 'api_key', ?)", value); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		got, ok, err := s.EngineValue(t.Context(), "test-engine", "api_key")
		if got != value || !ok || err != nil {
			t.Fatalf("legacy engine value did not round-trip: ok=%t, err=%v", ok, err)
		}
		assertEngineValueSealed(t, s, value)
	}
}

func TestEngineValueRejectsWrongMasterKey(t *testing.T) {
	const value = "synthetic-engine-value"
	s := openSealed(t)
	if err := s.SetEngineValue(t.Context(), "test-engine", "api_key", value); err != nil {
		t.Fatal(err)
	}
	original := s.sealer.Load()
	s.SetSealer(mustSealer(t, bytes.Repeat([]byte{2}, 32)))
	got, ok, err := s.EngineValue(t.Context(), "test-engine", "api_key")
	if got != "" || ok || err == nil {
		t.Fatal("reading with the wrong master key must fail without returning a value")
	}
	s.SetSealer(original)
	assertEngineValueSealed(t, s, value)
}

func TestEngineValueWriteRequiresSealer(t *testing.T) {
	s := openTemp(t)
	if err := s.SetEngineValue(t.Context(), "test-engine", "api_key", "synthetic-engine-value"); !errors.Is(err, ErrNoSealer) {
		t.Fatal("writing without a sealer must fail")
	}
}

func assertEngineValueSealed(t *testing.T, s *Store, plaintext string) {
	t.Helper()
	var raw string
	if err := s.db.QueryRowContext(t.Context(), "SELECT value FROM engine_state WHERE engine = 'test-engine' AND key = 'api_key'").Scan(&raw); err != nil {
		t.Fatal(err)
	}
	encoded, ok := strings.CutPrefix(raw, "v1:")
	if !ok || strings.Contains(raw, plaintext) {
		t.Fatal("engine value must have a version prefix and contain no plaintext")
	}
	sealed, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.sealer.Load().Open(sealed, "engine_state/test-engine/api_key")
	if got != plaintext || err != nil {
		t.Fatalf("engine value must open with the row's AAD: %v", err)
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
