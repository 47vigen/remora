package omniroute

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"golang.org/x/crypto/scrypt"
)

// fixtureSecret is the made-up STORAGE_ENCRYPTION_KEY of the fixture. It ends in "=" like a base64 key, so the
// fixture also exercises the candidate list LoadSecret builds.
const fixtureSecret = "fixture-secret-not-real="

// The tables of OmniRoute's 001_initial_schema.sql that the importer reads.
const schema001 = `
CREATE TABLE provider_connections (
  id TEXT PRIMARY KEY, provider TEXT NOT NULL, auth_type TEXT, name TEXT, email TEXT, priority INTEGER DEFAULT 0,
  is_active INTEGER DEFAULT 1, access_token TEXT, refresh_token TEXT, expires_at TEXT, token_expires_at TEXT,
  scope TEXT, project_id TEXT, test_status TEXT, error_code TEXT, last_error TEXT, last_error_at TEXT,
  last_error_type TEXT, last_error_source TEXT, backoff_level INTEGER DEFAULT 0, rate_limited_until TEXT,
  health_check_interval INTEGER, last_health_check_at TEXT, last_tested TEXT, api_key TEXT, id_token TEXT,
  provider_specific_data TEXT, expires_in INTEGER, display_name TEXT, global_priority INTEGER, default_model TEXT,
  token_type TEXT, consecutive_use_count INTEGER DEFAULT 0, rate_limit_protection INTEGER DEFAULT 0,
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE TABLE provider_nodes (
  id TEXT PRIMARY KEY, type TEXT NOT NULL, name TEXT NOT NULL, prefix TEXT, api_type TEXT, base_url TEXT,
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE TABLE combos (
  id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE, data TEXT NOT NULL, created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL);
CREATE TABLE api_keys (
  id TEXT PRIMARY KEY, name TEXT NOT NULL, key TEXT NOT NULL UNIQUE, machine_id TEXT, created_at TEXT NOT NULL);
`

// The api_keys columns later OmniRoute migrations add.
const schemaLater = `
ALTER TABLE api_keys ADD COLUMN revoked_at TEXT;
ALTER TABLE api_keys ADD COLUMN expires_at TEXT;
ALTER TABLE api_keys ADD COLUMN is_active INTEGER DEFAULT 1;
ALTER TABLE api_keys ADD COLUMN allowed_models TEXT;
ALTER TABLE api_keys ADD COLUMN allowed_combos TEXT;
ALTER TABLE api_keys ADD COLUMN model_access_mode TEXT DEFAULT 'all';
`

const ts = "2026-10-01T00:00:00.000Z"

// Plain values the fixture stores encrypted; tests compare against them.
const (
	fxORKey1     = "sk-or-fixture-1"
	fxORKey2     = "sk-or-fixture-2"
	fxOCKey      = "oc-fixture-key"
	fxAGAccess   = "ag-access-fixture"
	fxAGRefresh  = "ag-refresh-fixture"
	fxCXAccess   = "cx-access-fixture"
	fxCXRefresh  = "cx-refresh-fixture"
	fxClientKeyA = "sk-abc0123456789abcdef0123456789ab"
	fxClientKeyB = "sk-def0123456789abcdef0123456789cd"
)

// fixtureIDToken is an unsigned test JWT carrying the claims the Codex credential file needs.
var fixtureIDToken = testJWT(map[string]any{
	"email": "c@example.com",
	"https://api.openai.com/auth": map[string]any{
		"chatgpt_account_id": "acc-1",
		"chatgpt_plan_type":  "plus",
	},
})

func testJWT(claims map[string]any) string {
	enc := base64.RawURLEncoding
	h, _ := json.Marshal(map[string]string{"alg": "none", "typ": "JWT"})
	p, _ := json.Marshal(claims)
	return enc.EncodeToString(h) + "." + enc.EncodeToString(p) + "."
}

// keyCache keeps derived keys: scrypt is slow on purpose and the fixture encrypts many values.
var (
	keyMu    sync.Mutex
	keyCache = map[string][]byte{}
)

// encryptWith encrypts like OmniRoute's encrypt() with a key derived from secret and salt.
func encryptWith(t *testing.T, secret string, salt []byte, plain string) string {
	t.Helper()
	keyMu.Lock()
	key, ok := keyCache[secret+"\x00"+string(salt)]
	if !ok {
		var err error
		if key, err = scrypt.Key([]byte(secret), salt, 16384, 8, 1, 32); err != nil {
			keyMu.Unlock()
			t.Fatal(err)
		}
		keyCache[secret+"\x00"+string(salt)] = key
	}
	keyMu.Unlock()
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCMWithNonceSize(block, 16)
	if err != nil {
		t.Fatal(err)
	}
	iv := make([]byte, 16)
	if _, err := rand.Read(iv); err != nil {
		t.Fatal(err)
	}
	sealed := gcm.Seal(nil, iv, []byte(plain), nil)
	ct, tag := sealed[:len(sealed)-16], sealed[len(sealed)-16:]
	return prefix + hex.EncodeToString(iv) + ":" + hex.EncodeToString(ct) + ":" + hex.EncodeToString(tag)
}

func encrypt(t *testing.T, secret, plain string) string {
	return encryptWith(t, secret, []byte(staticSalt), plain)
}

func legacyEncrypt(t *testing.T, secret, plain string) string {
	sum := sha256.Sum256([]byte(secret))
	return encryptWith(t, secret, sum[:16], plain)
}

func createDB(t *testing.T, dir string, stmts ...string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(dir, "storage.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, s := range stmts {
		if _, err := db.ExecContext(context.Background(), s); err != nil {
			t.Fatalf("%v\n%s", err, s)
		}
	}
	return db
}

func exec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), q, args...); err != nil {
		t.Fatalf("%v\n%s", err, q)
	}
}

// buildFixture writes an OmniRoute data directory into dir: storage.sqlite with nodes, connections (secrets
// encrypted under fixtureSecret), client keys and combos, and a server.env holding the key.
func buildFixture(t *testing.T, dir string) {
	t.Helper()
	db := createDB(t, dir, schema001, schemaLater)
	s := fixtureSecret

	exec(t, db, `INSERT INTO provider_nodes (id, type, name, prefix, api_type, base_url, created_at, updated_at)
		VALUES ('openai-compatible-chat-n1', 'openai-compatible', 'Compat', 'oc', 'chat', 'https://llm.example.com/v1', ?, ?)`, ts, ts)

	conn := `INSERT INTO provider_connections (id, provider, auth_type, name, email, priority, is_active, api_key,
		access_token, refresh_token, id_token, expires_at, expires_in, project_id, provider_specific_data, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	exec(t, db, conn, "c1", "openrouter", "apikey", "or one", nil, 1, 1, encrypt(t, s, fxORKey1), nil, nil, nil, nil, nil, nil, nil, ts, ts)
	exec(t, db, conn, "c2", "openrouter", "apikey", "or two", nil, 2, 1, encrypt(t, s, fxORKey2), nil, nil, nil, nil, nil, nil, nil, ts, ts)
	exec(t, db, conn, "c3", "openai-compatible-chat-n1", "apikey", "compat", nil, 1, 1, encrypt(t, s, fxOCKey), nil, nil, nil, nil, nil, nil, nil, ts, ts)
	exec(t, db, conn, "c4", "antigravity", "oauth", "ag", "a@example.com", 1, 1, nil,
		encrypt(t, s, fxAGAccess), encrypt(t, s, fxAGRefresh), nil, "2026-10-14T13:00:00.000Z", 3599, "proj-test-1",
		`{"projectId":"proj-test-1"}`, ts, ts)
	exec(t, db, conn, "c5", "codex", "oauth", "cx", "c@example.com", 1, 1, nil,
		encrypt(t, s, fxCXAccess), encrypt(t, s, fxCXRefresh), encrypt(t, s, fixtureIDToken), "2026-10-20T12:00:00.000Z", 864000, nil, nil, ts, ts)
	exec(t, db, conn, "c6", "qwen", "oauth", "qw", "b@example.com", 1, 1, nil,
		encrypt(t, s, "qw-access"), nil, nil, nil, nil, nil, nil, ts, ts)
	exec(t, db, conn, "c7", "openrouter", "apikey", "or off", nil, 3, 0, encrypt(t, s, "sk-or-off"), nil, nil, nil, nil, nil, nil, nil, ts, ts)

	key := `INSERT INTO api_keys (id, name, key, created_at, revoked_at, expires_at, is_active, allowed_models,
		allowed_combos, model_access_mode) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	exec(t, db, key, "k1", "svc-a", fxClientKeyA, ts, nil, nil, 1, "[]", nil, "all")
	exec(t, db, key, "k2", "svc-b", fxClientKeyB, ts, nil, nil, 1, `["oc/model-x","openrouter/vendor/model-a"]`, `["fast"]`, "restricted")
	exec(t, db, key, "k3", "old", "sk-revoked0123456789abcdef01234567", ts, "2026-09-01T00:00:00.000Z", nil, 0, "[]", nil, "all")
	exec(t, db, key, "k4", "temp", "sk-expired0123456789abcdef01234567", ts, nil, "2026-10-01T00:00:00.000Z", 1, "[]", "[]", "all")

	combo := `INSERT INTO combos (id, name, data, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`
	exec(t, db, combo, "m1", "fast", `{"name":"fast","strategy":"round-robin","context_length":128000,"models":[
		"openrouter/vendor/model-a",
		{"model":"model-b","providerId":"openai-compatible-chat-n1"},
		{"model":"openrouter/vendor/model-c"},
		{"kind":"combo-ref","comboName":"base"}]}`, ts, ts)
	exec(t, db, combo, "m2", "base", `{"name":"base","strategy":"priority","models":["oc/model-x"]}`, ts, ts)
	exec(t, db, combo, "m3", "embed", `{"name":"embed","dimensions":1024,"models":["openrouter/vendor/embed-1"]}`, ts, ts)

	if err := os.WriteFile(filepath.Join(dir, "server.env"), []byte("PORT=20128\nSTORAGE_ENCRYPTION_KEY="+s+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// noEnv is an environment with nothing set.
func noEnv(string) (string, bool) { return "", false }
