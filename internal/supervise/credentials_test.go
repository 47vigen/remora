package supervise

import (
	"context"
	"path/filepath"
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

func TestEnsureCredentialsIsStable(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)

	c1, err := EnsureCredentials(ctx, s)
	if err != nil {
		t.Fatalf("EnsureCredentials: %v", err)
	}
	c2, err := EnsureCredentials(ctx, s)
	if err != nil {
		t.Fatalf("EnsureCredentials (second): %v", err)
	}
	if c1 != c2 {
		t.Fatalf("credentials changed between calls:\n%+v\n%+v", c1, c2)
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
	s := openStore(t)
	if err := s.SetEngineValue(ctx, "bifrost", "encryption_key", "keep-me"); err != nil {
		t.Fatalf("SetEngineValue: %v", err)
	}
	c, err := EnsureCredentials(ctx, s)
	if err != nil {
		t.Fatalf("EnsureCredentials: %v", err)
	}
	if c.BifrostEncryptionKey != "keep-me" {
		t.Errorf("BifrostEncryptionKey = %q, want keep-me", c.BifrostEncryptionKey)
	}
	if c.BifrostAdminPassword == "" || c.CLIProxyAPIKey == "" {
		t.Errorf("missing values were not generated: %+v", c)
	}
}
