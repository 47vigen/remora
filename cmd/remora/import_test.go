package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/47vigen/remora/internal/api/gen"
	"github.com/47vigen/remora/internal/config"
)

// fakeRemora is the slice of remora's management API that `remora import omniroute` calls, over an in-memory store.
type fakeRemora struct {
	mu        sync.Mutex
	providers map[string]bool
	aliases   map[string]bool
	keys      map[string]string // name → value
	accounts  []gen.AccountImport
	served    map[string][]string // provider → the models its accounts serve
	writes    int
	prunes    []bool
}

func newFakeRemora(t *testing.T) (*fakeRemora, *httptest.Server) {
	f := &fakeRemora{
		providers: map[string]bool{}, aliases: map[string]bool{"keep-me": true}, keys: map[string]string{},
		served: map[string][]string{"antigravity": {"gemini-3-flash", "gemini-3.8-flash-high"}},
	}
	mux := http.NewServeMux()
	config := func(apply bool) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			var req gen.ConfigRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			f.prunes = append(f.prunes, req.Prune != nil && *req.Prune)
			changes, err := f.change(req, apply)
			if err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(gen.Error{Error: gen.ErrorDetail{Type: "invalid_request", Code: "invalid_document", Message: err.Error()}})
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(gen.ConfigChanges{Changes: changes})
		}
	}
	auth := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer test-token" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			h(w, r)
		}
	}
	mux.HandleFunc("POST /api/config/plan", auth(config(false)))
	mux.HandleFunc("POST /api/config/apply", auth(config(true)))
	mux.HandleFunc("POST /api/accounts/import", auth(func(w http.ResponseWriter, r *http.Request) {
		var req gen.AccountImport
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		f.accounts = append(f.accounts, req)
		f.writes++
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(gen.Account{Provider: req.Provider, Status: "active"})
	}))
	mux.HandleFunc("GET /api/accounts", auth(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Query().Get("provider")
		list := gen.AccountList{Items: []gen.Account{}}
		if models, ok := f.served[p]; ok {
			refs := []string{}
			for _, m := range models { // the server answers user references, "<provider>/<model>"
				refs = append(refs, p+"/"+m)
			}
			list.Items = append(list.Items, gen.Account{Provider: p, Status: "active", Models: refs})
		}
		list.Total = len(list.Items)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(list)
	}))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return f, srv
}

// change plans (and with apply, makes) the creates a document needs; it never deletes.
func (f *fakeRemora) change(req gen.ConfigRequest, apply bool) ([]gen.Change, error) {
	doc, err := config.Parse([]byte(req.Document))
	if err != nil {
		return nil, err
	}
	var sec map[string]string
	if req.Secrets != nil {
		sec = *req.Secrets
	}
	changes := []gen.Change{}
	for _, p := range doc.Providers {
		if !f.providers[p.Name] {
			changes = append(changes, gen.Change{Op: gen.Create, Kind: gen.ChangeKindProvider, Name: p.Name, Fields: []string{}})
			if apply {
				f.providers[p.Name] = true
			}
		}
	}
	for _, a := range doc.Aliases {
		if !f.aliases[a.Name] {
			changes = append(changes, gen.Change{Op: gen.Create, Kind: gen.ChangeKindAlias, Name: a.Name, Fields: []string{}})
			if apply {
				f.aliases[a.Name] = true
			}
		}
	}
	for _, k := range doc.Keys {
		if _, ok := f.keys[k.Name]; !ok {
			changes = append(changes, gen.Change{Op: gen.Create, Kind: gen.ChangeKindKey, Name: k.Name, Fields: []string{}})
			if apply {
				ref, _ := config.SecretRef(k.Value)
				f.keys[k.Name] = sec[ref]
			}
		}
	}
	if apply {
		f.writes++
	}
	return changes, nil
}

// omniDir writes a small OmniRoute data directory with unencrypted values: an API-key provider, a client key, a
// combo and two OAuth accounts.
func omniDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "storage.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	idToken := "eyJhbGciOiJub25lIn0." + // {"alg":"none"}
		"eyJlbWFpbCI6ImNAZXhhbXBsZS5jb20iLCJodHRwczovL2FwaS5vcGVuYWkuY29tL2F1dGgiOnsiY2hhdGdwdF9hY2NvdW50X2lkIjoiYWNjLTEifX0."
	for _, q := range []string{
		`CREATE TABLE provider_nodes (id TEXT, type TEXT, name TEXT, prefix TEXT, base_url TEXT)`,
		`CREATE TABLE provider_connections (id TEXT, provider TEXT, auth_type TEXT, name TEXT, email TEXT,
			is_active INTEGER DEFAULT 1, access_token TEXT, refresh_token TEXT, expires_at TEXT, project_id TEXT,
			api_key TEXT, id_token TEXT, provider_specific_data TEXT, expires_in INTEGER)`,
		`CREATE TABLE combos (name TEXT, data TEXT)`,
		`CREATE TABLE api_keys (id TEXT, name TEXT, key TEXT)`,
		`INSERT INTO provider_connections (id, provider, auth_type, name, api_key) VALUES ('c1', 'openrouter', 'apikey', 'or', 'sk-or-import-secret')`,
		`INSERT INTO provider_connections (id, provider, auth_type, email, access_token, refresh_token, expires_at, expires_in, project_id)
			VALUES ('c2', 'antigravity', 'oauth', 'a@example.com', 'ag-access-secret', 'ag-refresh-secret', '2026-10-14T13:00:00Z', 3600, 'proj-1')`,
		`INSERT INTO provider_connections (id, provider, auth_type, email, access_token, refresh_token, id_token)
			VALUES ('c3', 'codex', 'oauth', 'c@example.com', 'cx-access-secret', 'cx-refresh-secret', '` + idToken + `')`,
		`INSERT INTO combos (name, data) VALUES ('fast', '{"models":["openrouter/vendor/model-a"]}')`,
		`INSERT INTO combos (name, data) VALUES ('smart', '{"models":["antigravity/gemini-3.8-flash-tiered",
			"antigravity/gemini-3.8-flash-high(8192)","antigravity/gemini-3-flash","codex/gpt-6"]}')`,
		`INSERT INTO api_keys (id, name, key) VALUES ('k1', 'svc-a', 'sk-abc-verbatim-0123456789abcdef01')`,
	} {
		if _, err := db.ExecContext(context.Background(), q); err != nil {
			t.Fatalf("%v\n%s", err, q)
		}
	}
	return dir
}

func importEnv(url string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		switch k {
		case "REMORA_URL":
			return url, true
		case "REMORA_TOKEN":
			return "test-token", true
		}
		return "", false
	}
}

var importSecrets = []string{
	"sk-or-import-secret", "ag-access-secret", "ag-refresh-secret", "cx-access-secret", "cx-refresh-secret",
	"sk-abc-verbatim-0123456789abcdef01",
}

func TestImportWithoutYes(t *testing.T) {
	f, srv := newFakeRemora(t)
	var out bytes.Buffer
	if err := runImport(context.Background(), []string{"omniroute", omniDir(t)}, &out, importEnv(srv.URL)); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	s := out.String()
	for _, want := range []string{
		"+ provider openrouter", "+ alias fast", "+ key svc-a", "2 accounts: antigravity a@example.com, codex c@example.com",
		"Codex refresh tokens are single-use: stop the old gateway before remora refreshes them.", "--yes",
		"warning: alias smart: member antigravity/gemini-3.8-flash-tiered: no antigravity account serves gemini-3.8-flash-tiered; " +
			"point it at a served model, e.g. antigravity/gemini-3.8-flash-high(8192)",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("stdout lacks %q:\n%s", want, s)
		}
	}
	for _, secret := range importSecrets {
		if strings.Contains(s, secret) {
			t.Errorf("stdout shows the secret %q", secret)
		}
	}
	if n := strings.Count(s, "no antigravity account serves"); n != 1 {
		t.Errorf("%d unserved-model warnings, want 1:\n%s", n, s)
	}
	if strings.Contains(s, "codex/gpt-6:") {
		t.Errorf("warned about a provider whose models are unknown:\n%s", s)
	}
	if f.writes != 0 || len(f.providers) != 0 || len(f.keys) != 0 || len(f.accounts) != 0 {
		t.Fatalf("the fake was changed: %+v", f)
	}
}

func TestImportYes(t *testing.T) {
	f, srv := newFakeRemora(t)
	var out bytes.Buffer
	if err := runImport(context.Background(), []string{"omniroute", "--yes", omniDir(t)}, &out, importEnv(srv.URL)); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if !f.providers["openrouter"] || !f.aliases["fast"] || f.keys["svc-a"] != "sk-abc-verbatim-0123456789abcdef01" {
		t.Fatalf("fake after import: %+v", f)
	}
	if !f.aliases["keep-me"] {
		t.Fatal("an alias OmniRoute does not have was deleted")
	}
	for _, p := range f.prunes {
		if p {
			t.Fatal("import asked for prune")
		}
	}
	if len(f.accounts) != 2 || f.accounts[0].Provider != "antigravity" || f.accounts[1].Provider != "codex" ||
		f.accounts[0].Credential["refresh_token"] != "ag-refresh-secret" || f.accounts[1].Credential["account_id"] != "acc-1" {
		t.Fatalf("accounts: %+v", f.accounts)
	}
	for _, secret := range importSecrets {
		if strings.Contains(out.String(), secret) {
			t.Errorf("stdout shows the secret %q", secret)
		}
	}
}

func TestImportErrors(t *testing.T) {
	_, srv := newFakeRemora(t)
	for _, args := range [][]string{nil, {"other", "dir"}, {"omniroute"}, {"omniroute", "a", "b"}, {"omniroute", "--nope", "a"}} {
		if err := runImport(context.Background(), args, &bytes.Buffer{}, importEnv(srv.URL)); err == nil {
			t.Errorf("%q: want a usage error", args)
		}
	}
	if err := runImport(context.Background(), []string{"omniroute", t.TempDir()}, &bytes.Buffer{}, importEnv(srv.URL)); err == nil {
		t.Error("a directory without storage.sqlite: want an error")
	}
	noToken := func(k string) (string, bool) {
		if k == "REMORA_URL" {
			return srv.URL, true
		}
		return "", false
	}
	err := runImport(context.Background(), []string{"omniroute", omniDir(t)}, &bytes.Buffer{}, noToken)
	if err == nil || !strings.Contains(err.Error(), "REMORA_TOKEN") {
		t.Errorf("no token: got %v", err)
	}
	// A server-side rejection (here: a wrong token) is reported, not swallowed.
	badToken := func(k string) (string, bool) {
		if k == "REMORA_TOKEN" {
			return "wrong", true
		}
		return noToken(k)
	}
	if err := runImport(context.Background(), []string{"omniroute", omniDir(t)}, &bytes.Buffer{}, badToken); err == nil {
		t.Error("rejected plan: want an error")
	}
}
