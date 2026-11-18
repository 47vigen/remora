package supervise

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/47vigen/remora/internal/store"
)

func openCredentialsStore(t *testing.T, path string) *store.Store {
	t.Helper()
	s, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	sl, err := store.NewSealer(bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	s.SetSealer(sl)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestEnsureCredentialsIsStable(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "remora.db")
	s := openCredentialsStore(t, path)

	c1, err := EnsureCredentials(ctx, s)
	if err != nil {
		t.Fatalf("EnsureCredentials: %v", err)
	}
	c2, err := EnsureCredentials(ctx, openCredentialsStore(t, path))
	if err != nil {
		t.Fatalf("EnsureCredentials (second): %v", err)
	}
	if c1 != c2 {
		t.Fatal("credentials changed after reopening the database")
	}
	if c1.BifrostAdminUser != "remora" {
		t.Errorf("BifrostAdminUser = %q, want remora", c1.BifrostAdminUser)
	}
	secrets := map[string]string{
		"BifrostAdminPassword":     c1.BifrostAdminPassword,
		"BifrostEncryptionKey":     c1.BifrostEncryptionKey,
		"CLIProxyManagementSecret": c1.CLIProxyManagementSecret,
		"CLIProxyAPIKey":           c1.CLIProxyAPIKey,
	}
	seen := map[string]string{}
	for name, v := range secrets {
		if len(v) < 43 {
			t.Errorf("%s has length %d, want >= 43", name, len(v))
		}
		if other, dup := seen[v]; dup {
			t.Errorf("%s equals %s", name, other)
		}
		seen[v] = name
	}
}

func TestEnsureCredentialsFillsOnlyMissing(t *testing.T) {
	ctx := context.Background()
	s := openCredentialsStore(t, filepath.Join(t.TempDir(), "remora.db"))
	if err := s.SetEngineValue(ctx, "bifrost", "encryption_key", "keep-me"); err != nil {
		t.Fatalf("SetEngineValue: %v", err)
	}
	c, err := EnsureCredentials(ctx, s)
	if err != nil {
		t.Fatalf("EnsureCredentials: %v", err)
	}
	if c.BifrostEncryptionKey != "keep-me" {
		t.Error("existing encryption key changed")
	}
	if c.BifrostAdminPassword == "" || c.CLIProxyAPIKey == "" {
		t.Error("missing values were not generated")
	}
}
