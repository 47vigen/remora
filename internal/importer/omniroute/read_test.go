package omniroute

import (
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

var fixtureNow = time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)

func TestReadFixture(t *testing.T) {
	dir := t.TempDir()
	buildFixture(t, dir)
	s, err := Read(context.Background(), dir, noEnv, fixtureNow)
	if err != nil {
		t.Fatal(err)
	}

	if len(s.Nodes) != 1 || s.Nodes[0] != (Node{ID: "openai-compatible-chat-n1", Type: "openai-compatible", Name: "Compat", Prefix: "oc", BaseURL: "https://llm.example.com/v1"}) {
		t.Fatalf("nodes: %+v", s.Nodes)
	}

	conns := map[string]Connection{}
	for _, c := range s.Connections {
		conns[c.ID] = c
	}
	if len(s.Connections) != 10 || s.Connections[0].ID != "c1" {
		t.Fatalf("connections: %+v", s.Connections)
	}
	if c := conns["c1"]; c.APIKey != fxORKey1 || c.Provider != "openrouter" || c.AuthType != "apikey" || !c.Active {
		t.Fatalf("c1: %+v", c)
	}
	if c := conns["c4"]; c.AccessToken != fxAGAccess || c.RefreshToken != fxAGRefresh || c.Email != "a@example.com" ||
		c.ProjectID != "proj-test-1" || c.ExpiresIn != 3599 || c.ExpiresAt != "2026-10-14T13:00:00.000Z" || c.Specific["projectId"] != "proj-test-1" {
		t.Fatalf("c4: %+v", c)
	}
	if c := conns["c5"]; c.IDToken != fixtureIDToken || c.RefreshToken != fxCXRefresh {
		t.Fatalf("c5: %+v", c)
	}
	if conns["c7"].Active {
		t.Fatal("c7 should be inactive")
	}

	keys := map[string]APIKey{}
	for _, k := range s.Keys {
		keys[k.Name] = k
	}
	if k := keys["svc-a"]; k.Key != fxClientKeyA || k.AccessMode != "all" || k.Revoked || k.Expired {
		t.Fatalf("svc-a: %+v", k)
	}
	if k := keys["svc-b"]; k.AccessMode != "restricted" ||
		!slices.Equal(k.AllowedModels, []string{"oc/model-x", "openrouter/vendor/model-a"}) || !slices.Equal(k.AllowedCombos, []string{"fast"}) {
		t.Fatalf("svc-b: %+v", k)
	}
	if !keys["old"].Revoked {
		t.Fatalf("old: %+v", keys["old"])
	}
	if k := keys["temp"]; !k.Expired || k.Revoked {
		t.Fatalf("temp: %+v", k)
	}

	if len(s.Combos) != 3 {
		t.Fatalf("combos: %+v", s.Combos)
	}
}

func fileSum(t *testing.T, p string) [32]byte {
	t.Helper()
	b, err := os.ReadFile(p) //nolint:gosec // G304: a test file.
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(b)
}

func TestReadIsReadOnly(t *testing.T) {
	dir := t.TempDir()
	buildFixture(t, dir)
	db := filepath.Join(dir, "storage.sqlite")
	if err := os.Chmod(db, 0o400); err != nil {
		t.Fatal(err)
	}
	before := fileSum(t, db)
	if _, err := Read(context.Background(), dir, noEnv, fixtureNow); err != nil {
		t.Fatal(err)
	}
	if fileSum(t, db) != before {
		t.Fatal("Read changed storage.sqlite")
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Stat(db + suffix); !os.IsNotExist(err) {
			t.Fatalf("Read created %s (%v)", db+suffix, err)
		}
	}
}

func TestReadOlderSchema(t *testing.T) {
	dir := t.TempDir()
	db := createDB(t, dir, schema001)
	exec(t, db, `INSERT INTO api_keys (id, name, key, created_at) VALUES ('k1', 'svc', 'sk-older0123456789abcdef0123456789', ?)`, ts)
	exec(t, db, `INSERT INTO provider_connections (id, provider, auth_type, api_key, created_at, updated_at)
		VALUES ('c1', 'openrouter', 'apikey', 'sk-or-plain', ?, ?)`, ts, ts)
	s, err := Read(context.Background(), dir, noEnv, fixtureNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Keys) != 1 || s.Keys[0].AccessMode != "all" || s.Keys[0].Revoked || s.Keys[0].Expired {
		t.Fatalf("keys: %+v", s.Keys)
	}
	// An unencrypted database needs no key.
	if len(s.Connections) != 1 || s.Connections[0].APIKey != "sk-or-plain" || !s.Connections[0].Active {
		t.Fatalf("connections: %+v", s.Connections)
	}
}

func TestReadErrors(t *testing.T) {
	if _, err := Read(context.Background(), t.TempDir(), noEnv, fixtureNow); err == nil {
		t.Fatal("missing storage.sqlite: want an error")
	}

	dir := t.TempDir()
	buildFixture(t, dir)
	if err := os.Remove(filepath.Join(dir, "server.env")); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(context.Background(), dir, noEnv, fixtureNow); err == nil {
		t.Fatal("encrypted database without a key: want an error")
	}

	dir = t.TempDir()
	createDB(t, dir, `CREATE TABLE api_keys (id TEXT, name TEXT)`)
	if _, err := Read(context.Background(), dir, noEnv, fixtureNow); err == nil {
		t.Fatal("not an OmniRoute database: want an error")
	}
}
