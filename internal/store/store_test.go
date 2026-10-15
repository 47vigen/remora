package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
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
	if version != len(files) || version != 5 {
		t.Errorf("user_version = %d, want %d (files) == 5", version, len(files))
	}

	for _, table := range []string{
		"settings", "engine_state", "providers", "provider_keys", "client_keys", "aliases", "alias_members", "engine_sync",
		"accounts", "sessions", "api_tokens",
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
