package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/47vigen/remora/internal/alias"
	"github.com/47vigen/remora/internal/api"
	"github.com/47vigen/remora/internal/api/gen"
	"github.com/47vigen/remora/internal/auth"
	"github.com/47vigen/remora/internal/reconcile"
	"github.com/47vigen/remora/internal/reconcile/reconciletest"
	"github.com/47vigen/remora/internal/store"
)

const testAdminPassword = "test-admin-password"

// remoraHarness is a management API over a real store, the real adapters and a fake front door.
type remoraHarness struct {
	srv     *httptest.Server
	token   string
	aliases api.Aliases
	keys    api.Keys

	mu    sync.Mutex
	plans []gen.ConfigRequest // every plan request body
}

func newRemora(t *testing.T) *remoraHarness {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "remora.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	sl, err := store.NewSealer(bytes.Repeat([]byte{3}, 32))
	if err != nil {
		t.Fatal(err)
	}
	st.SetSealer(sl)
	if _, err := auth.EnsureAdmin(ctx, st, testAdminPassword); err != nil {
		t.Fatal(err)
	}
	authSvc := auth.NewService(st, nil)
	_, token, err := authSvc.CreateToken(ctx, "cli")
	if err != nil {
		t.Fatal(err)
	}
	rec := &reconcile.Reconciler{Store: st, Engine: reconciletest.New()}
	h := &remoraHarness{token: token, aliases: api.NewAliases(st, rec, nil), keys: api.NewKeys(st, rec)}
	providers := api.NewProviders(st, rec, nil, nil)
	mgmt := api.New(api.Deps{Providers: providers, Aliases: h.aliases, Keys: h.keys, Batch: rec.Batch, Auth: authSvc}).Handler()
	h.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/config/plan" {
			b, _ := io.ReadAll(r.Body)
			var req gen.ConfigRequest
			_ = json.Unmarshal(b, &req)
			h.mu.Lock()
			h.plans = append(h.plans, req)
			h.mu.Unlock()
			r.Body = io.NopCloser(bytes.NewReader(b))
		}
		mgmt.ServeHTTP(w, r)
	}))
	t.Cleanup(h.srv.Close)

	// A provider, an alias and a client key to start from.
	if _, err := providers.Create(ctx, api.Provider{
		Name: "or", Kind: "openai-compatible", BaseURL: "http://upstream.test/or", Keys: []api.ProviderKey{{Name: "key-1", Value: "sk-or-stored"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.aliases.Put(ctx, alias.Alias{Name: "fast", Kind: alias.KindChat, Members: []alias.Member{{Ref: "or/vendor/m"}}}, true); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.keys.Create(ctx, "full", []string{"*"}, "sk-client-stored-value", true); err != nil {
		t.Fatal(err)
	}
	return h
}

// env is the CLI's environment: the harness's URL and token, plus extra.
func (h *remoraHarness) env(extra map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		switch k {
		case "REMORA_URL":
			return h.srv.URL, true
		case "REMORA_TOKEN":
			return h.token, true
		}
		v, ok := extra[k]
		return v, ok
	}
}

func (h *remoraHarness) planRequests() []gen.ConfigRequest {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.plans
}

func (h *remoraHarness) hasAlias(t *testing.T, name string) bool {
	t.Helper()
	_, err := h.aliases.Get(context.Background(), name)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		t.Fatal(err)
	}
	return err == nil
}

const addAliasDoc = `
providers:
  - {name: or, kind: openai-compatible, base_url: http://upstream.test/or, keys: [{name: key-1, value: env:REMORA_PROVIDER_OR_KEY_1}]}
aliases:
  - {name: fast, kind: chat, members: [or/vendor/m]}
  - {name: x, kind: chat, members: [fast]}
keys:
  - {name: full, allow: ["*"]}
`

func writeFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "remora.yaml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestApplyWithoutYesChangesNothing(t *testing.T) {
	h := newRemora(t)
	var out bytes.Buffer
	if err := runApply(context.Background(), []string{"-f", writeFile(t, addAliasDoc)}, &out, h.env(nil)); err != nil {
		t.Fatalf("runApply: %v\n%s", err, out.String())
	}
	if s := out.String(); !strings.Contains(s, "+ alias x") || !strings.Contains(s, "Run again with --yes to apply.") {
		t.Errorf("stdout:\n%s", s)
	}
	if h.hasAlias(t, "x") {
		t.Error("apply without --yes created the alias")
	}
}

func TestApplyYes(t *testing.T) {
	h := newRemora(t)
	var out bytes.Buffer
	if err := runApply(context.Background(), []string{"-f", writeFile(t, addAliasDoc), "--yes"}, &out, h.env(nil)); err != nil {
		t.Fatalf("runApply: %v\n%s", err, out.String())
	}
	if !h.hasAlias(t, "x") {
		t.Fatalf("alias x not created:\n%s", out.String())
	}
	if s := out.String(); !strings.Contains(s, "+ alias x") || !strings.Contains(s, "Applied 1 change.") {
		t.Errorf("stdout:\n%s", s)
	}
	out.Reset()
	if err := runApply(context.Background(), []string{"-f", writeFile(t, addAliasDoc), "--yes"}, &out, h.env(nil)); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != "No changes." {
		t.Errorf("second apply:\n%s", out.String())
	}
}

func TestApplyPrune(t *testing.T) {
	h := newRemora(t)
	doc := strings.Replace(addAliasDoc, "keys:\n  - {name: full, allow: [\"*\"]}\n", "keys: []\n", 1)
	var out bytes.Buffer
	if err := runApply(context.Background(), []string{"-f", writeFile(t, doc), "--prune=false"}, &out, h.env(nil)); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "- key full") {
		t.Errorf("--prune=false plans a delete:\n%s", out.String())
	}
	out.Reset()
	if err := runApply(context.Background(), []string{"-f", writeFile(t, doc)}, &out, h.env(nil)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "- key full") {
		t.Errorf("default prune does not delete:\n%s", out.String())
	}
}

func TestApplyPrintsGeneratedKey(t *testing.T) {
	h := newRemora(t)
	doc := addAliasDoc + "  - {name: new-svc, allow: [fast]}\n"
	var out bytes.Buffer
	if err := runApply(context.Background(), []string{"-f", writeFile(t, doc), "--yes"}, &out, h.env(nil)); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "+ key new-svc") || !strings.Contains(out.String(), "new-svc: sk-") {
		t.Errorf("stdout:\n%s", out.String())
	}
}

func TestApplySendsOnlyReferencedEnv(t *testing.T) {
	h := newRemora(t)
	env := h.env(map[string]string{"REMORA_PROVIDER_OR_KEY_1": "sk-or-rotated", "UNRELATED": "nope"})
	var out bytes.Buffer
	if err := runApply(context.Background(), []string{"-f", writeFile(t, addAliasDoc)}, &out, env); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	plans := h.planRequests()
	if len(plans) != 1 || plans[0].Secrets == nil {
		t.Fatalf("plans = %+v", plans)
	}
	if got := *plans[0].Secrets; !reflect.DeepEqual(got, map[string]string{"REMORA_PROVIDER_OR_KEY_1": "sk-or-rotated"}) {
		t.Errorf("secrets = %v", got)
	}
	if !strings.Contains(out.String(), "~ provider or (keys)") {
		t.Errorf("a rotated secret is not planned:\n%s", out.String())
	}
	if strings.Contains(out.String(), "sk-or-rotated") {
		t.Error("stdout shows the secret")
	}
}

func TestApplyErrors(t *testing.T) {
	h := newRemora(t)
	ctx := context.Background()
	for _, args := range [][]string{nil, {"-f"}, {"extra"}, {"-f", filepath.Join(t.TempDir(), "missing.yaml")}} {
		if err := runApply(ctx, args, io.Discard, h.env(nil)); err == nil {
			t.Errorf("%q: want an error", args)
		}
	}
	// A document that fails locally never reaches the server.
	if err := runApply(ctx, []string{"-f", writeFile(t, "version: 2\n")}, io.Discard, h.env(nil)); err == nil || !strings.Contains(err.Error(), "version") {
		t.Errorf("invalid document: %v", err)
	}
	if len(h.planRequests()) != 0 {
		t.Error("an invalid document was sent")
	}
	// A server-side refusal is reported: a new provider whose secret is unset.
	doc := "providers:\n  - {name: new, kind: soniox, keys: [{name: main, value: env:NEW_KEY}]}\n"
	if err := runApply(ctx, []string{"-f", writeFile(t, doc), "--prune=false"}, io.Discard, h.env(nil)); err == nil || !strings.Contains(err.Error(), "env:NEW_KEY") {
		t.Errorf("unset secret: %v", err)
	}
	var out bytes.Buffer
	if err := runApply(ctx, []string{"--help"}, &out, h.env(nil)); !errors.Is(err, errHelp) || !strings.Contains(out.String(), "-yes") {
		t.Errorf("--help = %v\n%s", err, out.String())
	}
}

func TestExportThenApplyNoChanges(t *testing.T) {
	h := newRemora(t)
	t.Setenv("REMORA_URL", h.srv.URL)
	t.Setenv("REMORA_TOKEN", h.token)
	path := filepath.Join(t.TempDir(), "remora.yaml")
	var out bytes.Buffer
	if err := runExport(context.Background(), []string{"-o", path}, &out); err != nil {
		t.Fatalf("runExport: %v", err)
	}
	b, err := os.ReadFile(path) //nolint:gosec // G304: a file in the test's temporary directory.
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "sk-or-stored") || strings.Contains(string(b), "sk-client-stored-value") ||
		!strings.Contains(string(b), "env:REMORA_PROVIDER_OR_KEY_1") {
		t.Fatalf("exported:\n%s", b)
	}
	out.Reset()
	if err := runApply(context.Background(), []string{"-f", path}, &out, h.env(nil)); err != nil {
		t.Fatalf("runApply: %v", err)
	}
	if strings.TrimSpace(out.String()) != "No changes." {
		t.Errorf("apply of the export:\n%s", out.String())
	}

	// Without -o the document goes to stdout.
	out.Reset()
	if err := runExport(context.Background(), nil, &out); err != nil {
		t.Fatal(err)
	}
	if out.String() != string(b) {
		t.Errorf("stdout export differs from the file:\n%s", out.String())
	}
}

func TestClientAuthOrder(t *testing.T) {
	h := newRemora(t)
	ctx := context.Background()
	export := func(c *gen.ClientWithResponses) int {
		t.Helper()
		resp, err := c.ExportConfig(ctx)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}

	// The token wins over the password: a good token with a wrong password works...
	c, err := newClient(ctx, h.srv.URL, h.token, "wrong-password")
	if err != nil || export(c) != http.StatusOK {
		t.Errorf("token + wrong password: %v", err)
	}
	// ...and a wrong token with the right password does not.
	c, err = newClient(ctx, h.srv.URL, "rmt_wrong", testAdminPassword)
	if err != nil || export(c) != http.StatusUnauthorized {
		t.Errorf("wrong token + password: %v", err)
	}
	// The password alone signs in.
	c, err = newClient(ctx, h.srv.URL, "", testAdminPassword)
	if err != nil || export(c) != http.StatusOK {
		t.Errorf("password: %v", err)
	}
	if _, err := newClient(ctx, h.srv.URL, "", "wrong-password"); err == nil {
		t.Error("wrong password: want an error")
	}
	if _, err := newClient(ctx, h.srv.URL, "", ""); err == nil || !strings.Contains(err.Error(), "REMORA_TOKEN") {
		t.Errorf("neither: %v", err)
	}
}

func TestClientURL(t *testing.T) {
	env := func(m map[string]string) func(string) (string, bool) {
		return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
	}
	for _, tt := range []struct {
		flag string
		env  map[string]string
		want string
	}{
		{"http://flag.test", map[string]string{"REMORA_URL": "http://env.test"}, "http://flag.test"},
		{"", map[string]string{"REMORA_URL": "http://env.test", "REMORA_LISTEN": ":9"}, "http://env.test"},
		{"", map[string]string{"REMORA_LISTEN": "0.0.0.0:9191"}, "http://127.0.0.1:9191"},
		{"", nil, "http://127.0.0.1:8080"},
	} {
		got, err := clientURL(tt.flag, env(tt.env))
		if err != nil || got != tt.want {
			t.Errorf("clientURL(%q, %v) = %q, %v; want %q", tt.flag, tt.env, got, err, tt.want)
		}
	}
}

func TestScrubWriter(t *testing.T) {
	var b bytes.Buffer
	msg := "reconcile: bifrost: POST /api/providers: status 500 (key sk-bf-abc)\n"
	n, err := scrubWriter{&b}.Write([]byte(msg))
	if err != nil || n != len(msg) {
		t.Fatalf("Write = (%d, %v)", n, err)
	}
	if got := strings.ToLower(b.String()); strings.Contains(got, "bifrost") || strings.Contains(got, "sk-bf-") {
		t.Errorf("scrubbed = %q", b.String())
	}
}
