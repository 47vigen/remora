package auth

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/47vigen/remora/internal/store"
)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "remora.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func mustCheck(t *testing.T, s *store.Store, password string, want bool) {
	t.Helper()
	got, err := CheckAdminPassword(context.Background(), s, password)
	if err != nil {
		t.Fatalf("CheckAdminPassword(%q): %v", password, err)
	}
	if got != want {
		t.Errorf("CheckAdminPassword(%q) = %v, want %v", password, got, want)
	}
}

func TestEnsureAdminGeneratesOnce(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)

	gen, err := EnsureAdmin(ctx, s, "")
	if err != nil {
		t.Fatalf("EnsureAdmin: %v", err)
	}
	if len(gen) < 32 {
		t.Fatalf("generated password %q is shorter than 32", gen)
	}
	gen2, err := EnsureAdmin(ctx, s, "")
	if err != nil {
		t.Fatalf("second EnsureAdmin: %v", err)
	}
	if gen2 != "" {
		t.Errorf("second EnsureAdmin generated %q, want \"\"", gen2)
	}

	mustCheck(t, s, gen, true)
	mustCheck(t, s, "wrong", false)

	stored, ok, err := s.Setting(ctx, SettingAdminPasswordHash)
	if err != nil || !ok {
		t.Fatalf("Setting = (%q, %v, %v)", stored, ok, err)
	}
	if stored == gen || !strings.HasPrefix(stored, "$2") {
		t.Errorf("stored value %q is not a bcrypt hash", stored)
	}
}

func TestEnsureAdminUsesEnv(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)

	gen, err := EnsureAdmin(ctx, s, "from-env")
	if err != nil {
		t.Fatalf("EnsureAdmin: %v", err)
	}
	if gen != "" {
		t.Errorf("EnsureAdmin = %q, want \"\"", gen)
	}
	mustCheck(t, s, "from-env", true)
}

func TestEnsureAdminEnvIgnoredOnceAdminExists(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)

	gen, err := EnsureAdmin(ctx, s, "")
	if err != nil {
		t.Fatalf("EnsureAdmin: %v", err)
	}
	gen2, err := EnsureAdmin(ctx, s, "new-env")
	if err != nil {
		t.Fatalf("second EnsureAdmin: %v", err)
	}
	if gen2 != "" {
		t.Errorf("second EnsureAdmin = %q, want \"\"", gen2)
	}
	mustCheck(t, s, gen, true)
	mustCheck(t, s, "new-env", false)
}

func TestCheckAdminPasswordWithoutHash(t *testing.T) {
	mustCheck(t, openStore(t), "x", false)
}
