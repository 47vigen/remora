//go:build engine

// Contract tests against the real OAuth engine binary (REMORA_CLIPROXYAPI_BIN). They prove the client's paths,
// fields and status codes, the behaviour the fake copies, and the Codex parameter rewrites remora announces in
// x-remora-dropped-params. They use no account and reach no provider: the engine's outbound proxy is a closed
// loopback port, and the Codex upstream is an in-process server.
package cliproxyapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/47vigen/remora/internal/engine/cliproxyapi"
	"github.com/47vigen/remora/internal/proxy"
)

const (
	contractSecret = "contract-management-secret"
	contractKey    = "k"
)

type engine struct {
	url    string
	client *cliproxyapi.Client
}

// freePort returns a loopback port that was free a moment ago.
func freePort(t *testing.T) int {
	t.Helper()
	var lc net.ListenConfig
	l, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	if err := l.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return port
}

// syncBuffer collects the engine's output for the failure log.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// startEngine runs the engine with a fresh v8 config and data directory and stops it when the test ends.
func startEngine(t *testing.T) engine {
	t.Helper()
	bin := os.Getenv("REMORA_CLIPROXYAPI_BIN")
	if bin == "" {
		t.Skip("REMORA_CLIPROXYAPI_BIN is not set")
	}
	dir := t.TempDir()
	authDir := filepath.Join(dir, "auths")
	if err := os.Mkdir(authDir, 0o700); err != nil {
		t.Fatal(err)
	}
	port := freePort(t)
	// Every outbound call the engine makes on its own (catalogs, version checks, token exchanges) goes to a
	// closed loopback port, so the test never reaches a provider.
	deadProxy := fmt.Sprintf("http://127.0.0.1:%d", freePort(t))
	cfg := fmt.Sprintf(`config-version: 8
server:
  host: 127.0.0.1
  port: %d
management:
  secret-key: %q
  disable-control-panel: true
access:
  api-keys: [%q]
oauth:
  auth-dir: %q
requests:
  proxy-url: %q
`, port, contractSecret, contractKey, authDir, deadProxy)
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, bin, "-config", cfgPath) //nolint:gosec // G204, G702: the engine binary under test
	cmd.Dir = dir
	// The engine also makes outbound calls through the environment's proxy settings; point those at the closed
	// port too. Loopback is never proxied.
	cmd.Env = append(withoutProxyEnv(os.Environ()),
		"HTTP_PROXY="+deadProxy, "HTTPS_PROXY="+deadProxy, "http_proxy="+deadProxy, "https_proxy="+deadProxy)
	var out syncBuffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("start engine: %v", err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	t.Cleanup(func() {
		cancel()
		<-done
		if t.Failed() {
			t.Logf("engine output:\n%s", out.String())
		}
	})

	base := "http://127.0.0.1:" + strconv.Itoa(port)
	deadline := time.Now().Add(30 * time.Second)
	for {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/", nil)
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				break
			}
		}
		select {
		case <-done:
			t.Fatalf("engine exited before it answered:\n%s", out.String())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("engine did not answer GET / within 30s:\n%s", out.String())
		}
		time.Sleep(100 * time.Millisecond)
	}
	return engine{url: base, client: cliproxyapi.New(base, contractSecret, nil)}
}

// withoutProxyEnv drops the proxy variables from env.
func withoutProxyEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		switch strings.ToUpper(k) {
		case "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY":
			continue
		}
		out = append(out, kv)
	}
	return out
}

func wantStatus(t *testing.T, what string, err error, status int) {
	t.Helper()
	var e *cliproxyapi.Error
	if !errors.As(err, &e) || e.StatusCode != status {
		t.Errorf("%s: err = %v, want *Error %d", what, err, status)
	}
}

func TestContractOAuthStart(t *testing.T) {
	e := startEngine(t)
	ctx := context.Background()

	_, err := cliproxyapi.New(e.url, "wrong", nil).Credentials(ctx)
	wantStatus(t, "wrong secret", err, http.StatusUnauthorized)

	start, err := e.client.StartOAuth(ctx, "codex")
	if err != nil {
		t.Fatalf("StartOAuth(codex): %v", err)
	}
	u, err := url.Parse(start.URL)
	if err != nil || u.Host != "auth.openai.com" {
		t.Errorf("codex URL = %q, want host auth.openai.com", start.URL)
	}
	if start.State == "" || start.Flow != "" || start.UserCode != "" || start.ExpiresIn != 0 {
		t.Errorf("codex start = %+v, want URL and State only", start)
	}
	if u != nil && u.Query().Get("state") != start.State {
		t.Errorf("URL state %q != State %q", u.Query().Get("state"), start.State)
	}
	st, err := e.client.OAuthStatus(ctx, start.State)
	if err != nil || st != (cliproxyapi.OAuthStatus{Status: "wait"}) {
		t.Errorf("fresh status = %+v, %v, want wait", st, err)
	}
	if err := e.client.CancelOAuth(ctx, start.State); err != nil {
		t.Fatalf("CancelOAuth: %v", err)
	}
	st, err = e.client.OAuthStatus(ctx, start.State)
	if err != nil || st != (cliproxyapi.OAuthStatus{Status: "error", Error: "unknown or expired state"}) {
		t.Errorf("status after cancel = %+v, %v, want error unknown or expired state", st, err)
	}
	if err := e.client.CancelOAuth(ctx, start.State); err != nil {
		t.Errorf("second CancelOAuth: %v", err)
	}

	_, err = e.client.OAuthStatus(ctx, "a/b")
	wantStatus(t, "status of an invalid state", err, http.StatusBadRequest)
	_, err = e.client.StartOAuth(ctx, "nope")
	wantStatus(t, "unknown provider", err, http.StatusNotFound)
}

func TestContractCallback(t *testing.T) {
	e := startEngine(t)
	ctx := context.Background()
	start, err := e.client.StartOAuth(ctx, "codex")
	if err != nil {
		t.Fatalf("StartOAuth: %v", err)
	}
	t.Cleanup(func() { _ = e.client.CancelOAuth(context.Background(), start.State) })

	err = e.client.SubmitOAuthCallback(ctx, start.State, "http://localhost:1455/auth/callback?state="+start.State)
	wantStatus(t, "callback without code", err, http.StatusBadRequest)
	err = e.client.SubmitOAuthCallback(ctx, "unknown", "http://localhost:1455/auth/callback?code=x&state=unknown")
	wantStatus(t, "callback for an unknown state", err, http.StatusNotFound)

	if err := e.client.SubmitOAuthCallback(ctx, start.State,
		"http://localhost:1455/auth/callback?code=x&state="+start.State); err != nil {
		t.Fatalf("SubmitOAuthCallback: %v", err)
	}
	// Acceptance is not success: the code exchange cannot reach the provider, so the login ends in "error".
	deadline := time.Now().Add(20 * time.Second)
	for {
		st, err := e.client.OAuthStatus(ctx, start.State)
		if err != nil {
			t.Fatalf("OAuthStatus: %v", err)
		}
		if st.Status == "error" {
			if st.Error == "" {
				t.Error("error status without a message")
			}
			break
		}
		if st.Status != "wait" || time.Now().After(deadline) {
			t.Fatalf("status = %+v, want wait then error", st)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func TestContractCredentials(t *testing.T) {
	e := startEngine(t)
	ctx := context.Background()
	const name = "codex-c@x.json"
	if err := e.client.UploadCredential(ctx, name,
		[]byte(`{"type":"codex","email":"c@x","access_token":"t"}`)); err != nil {
		t.Fatalf("UploadCredential: %v", err)
	}
	wantStatus(t, "upload without .json", e.client.UploadCredential(ctx, "codex.txt", []byte(`{}`)), http.StatusBadRequest)

	find := func() (cliproxyapi.Credential, bool) {
		t.Helper()
		creds, err := e.client.Credentials(ctx)
		if err != nil {
			t.Fatalf("Credentials: %v", err)
		}
		for _, c := range creds {
			if c.Name == name {
				return c, true
			}
		}
		return cliproxyapi.Credential{}, false
	}
	c, ok := find()
	if !ok {
		t.Fatalf("uploaded credential %s not listed", name)
	}
	if c.ID != name || c.Provider != "codex" || c.Email != "c@x" || c.Label != "c@x" || c.Status != "active" ||
		c.AuthIndex == "" || c.Disabled || c.Unavailable || c.Cooldowns == nil || c.Quota.Signals == nil {
		t.Errorf("listed credential = %+v", c)
	}

	// The engine can drop an uploaded credential's model registration; AwaitCredentialModels nudges it as remora does.
	models, err := e.client.AwaitCredentialModels(ctx, name, "codex", 5*time.Second)
	if err != nil || len(models) == 0 {
		t.Errorf("CredentialModels = %v, %v, want some", models, err)
	}
	if models, err := e.client.CredentialModels(ctx, "nope.json"); err != nil || len(models) != 0 {
		t.Errorf("CredentialModels(unknown) = %v, %v, want none", models, err)
	}

	if err := e.client.SetCredentialPrefix(ctx, name, "codex"); err != nil {
		t.Errorf("SetCredentialPrefix: %v", err)
	}
	if err := e.client.SetCredentialDisabled(ctx, name, true); err != nil {
		t.Fatalf("SetCredentialDisabled: %v", err)
	}
	if c, _ := find(); !c.Disabled || c.Status != "disabled" {
		t.Errorf("after disable = %+v", c)
	}
	if err := e.client.SetCredentialDisabled(ctx, name, false); err != nil {
		t.Fatalf("SetCredentialDisabled(false): %v", err)
	}
	if c, _ := find(); c.Disabled || c.Status != "active" {
		t.Errorf("after enable = %+v", c)
	}
	if err := e.client.RefreshCredential(ctx, name); err != nil {
		t.Errorf("RefreshCredential: %v", err)
	}
	if err := e.client.ResetCooldown(ctx, c.AuthIndex); err != nil {
		t.Errorf("ResetCooldown: %v", err)
	}
	if err := e.client.DeleteCredential(ctx, name); err != nil {
		t.Fatalf("DeleteCredential: %v", err)
	}
	if _, ok := find(); ok {
		t.Error("credential still listed after delete")
	}

	wantStatus(t, "status of a missing credential", e.client.SetCredentialDisabled(ctx, name, true), http.StatusNotFound)
	wantStatus(t, "fields of a missing credential", e.client.SetCredentialPrefix(ctx, name, "codex"), http.StatusNotFound)
	wantStatus(t, "refresh of a missing credential", e.client.RefreshCredential(ctx, name), http.StatusNotFound)
	wantStatus(t, "reset of a missing auth index", e.client.ResetCooldown(ctx, c.AuthIndex), http.StatusNotFound)
	wantStatus(t, "delete of a missing credential", e.client.DeleteCredential(ctx, name), http.StatusNotFound)
}

// reconcilePatch is the settings patch accounts reconciliation sends (plan 03, Task 5).
func reconcilePatch(clientKey string) map[string]any {
	return map[string]any{
		"access": map[string]any{"api-keys": []any{clientKey}},
		"routing": map[string]any{
			"strategy":           "round-robin",
			"force-model-prefix": true,
			"cooldown":           map[string]any{"save-cooldown-status": true},
		},
		"upstream": map[string]any{"codex": map[string]any{"model-level-cooling": true}},
	}
}

// containsAt reports whether every leaf of want sits at the same path in got.
func containsAt(t *testing.T, path string, got, want any) {
	t.Helper()
	if w, ok := want.(map[string]any); ok {
		g, ok := got.(map[string]any)
		if !ok {
			t.Errorf("%s = %v, want an object", path, got)
			return
		}
		for k, v := range w {
			containsAt(t, path+"."+k, g[k], v)
		}
		return
	}
	wj, _ := json.Marshal(want)
	gj, _ := json.Marshal(got)
	if !bytes.Equal(wj, gj) {
		t.Errorf("%s = %s, want %s", path, gj, wj)
	}
}

func TestContractConfigPatch(t *testing.T) {
	e := startEngine(t)
	ctx := context.Background()
	patch := reconcilePatch("client-key-1")
	if err := e.client.PatchConfig(ctx, patch); err != nil {
		t.Fatalf("PatchConfig: %v", err)
	}
	raw, err := e.client.Config(ctx)
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	// Round-trip the patch through JSON so numbers and lists compare as the engine's answer does.
	var want map[string]any
	pj, _ := json.Marshal(patch)
	_ = json.Unmarshal(pj, &want)
	containsAt(t, "config", got, want)

	// The management secret survives replacing the client keys, and PATCH replaces lists.
	if err := e.client.PatchConfig(ctx, reconcilePatch("client-key-2")); err != nil {
		t.Fatalf("second PatchConfig: %v", err)
	}
	raw, _ = e.client.Config(ctx)
	got = nil
	_ = json.Unmarshal(raw, &got)
	containsAt(t, "config", got, map[string]any{"access": map[string]any{"api-keys": []any{"client-key-2"}}})

	wantStatus(t, "non-object patch", e.client.PatchConfig(ctx, []string{"x"}), http.StatusBadRequest)
}

// codexUpstream stands in for the Codex upstream: it records each body POSTed to /responses and answers a minimal
// completed response stream.
type codexUpstream struct {
	*httptest.Server
	mu     sync.Mutex
	bodies [][]byte
}

func newCodexUpstream(t *testing.T) *codexUpstream {
	t.Helper()
	u := &codexUpstream{}
	u.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/responses" {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		u.mu.Lock()
		u.bodies = append(u.bodies, body)
		u.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		resp := `{"id":"resp_1","object":"response","created_at":1,"status":"completed","model":"gpt-test",` +
			`"output":[{"type":"message","id":"msg_1","role":"assistant","status":"completed",` +
			`"content":[{"type":"output_text","text":"ok","annotations":[]}]}],` +
			`"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`
		_, _ = fmt.Fprintf(w, "event: response.created\ndata: {\"type\":\"response.created\",\"sequence_number\":0,\"response\":%s}\n\n", resp)
		_, _ = fmt.Fprintf(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"sequence_number\":1,\"response\":%s}\n\n", resp)
	}))
	t.Cleanup(u.Close)
	return u
}

func (u *codexUpstream) last(t *testing.T) map[string]any {
	t.Helper()
	u.mu.Lock()
	defer u.mu.Unlock()
	if len(u.bodies) == 0 {
		t.Fatal("the Codex upstream received no request")
	}
	var m map[string]any
	if err := json.Unmarshal(u.bodies[len(u.bodies)-1], &m); err != nil {
		t.Fatalf("recorded body: %v", err)
	}
	return m
}

// withCodexUpstream registers a Codex API-key group with prefix "codex" and model "gpt-test" pointing at a fresh
// fake upstream. Codex API-key groups run the same translator and executor as Codex OAuth credentials.
func withCodexUpstream(t *testing.T, e engine) *codexUpstream {
	t.Helper()
	up := newCodexUpstream(t)
	// The engine answers HTTP a moment before its config watcher runs, and a configuration write in that window is
	// saved but not applied until the file changes again. So the group is written again, under a new name, until
	// the prefixed model is listed.
	deadline := time.Now().Add(30 * time.Second)
	for attempt := 0; ; attempt++ {
		patch := map[string]any{"api-keys": map[string]any{"codex": []any{map[string]any{
			"name":      fmt.Sprintf("contract-%d", attempt),
			"prefix":    "codex",
			"base-url":  up.URL,
			"proxy-url": "direct", // the global proxy is a closed port; the fake upstream is on loopback
			"models":    []any{map[string]any{"name": "gpt-test"}},
			"keys":      []any{map[string]any{"api-key": "x"}},
		}}}}
		if err := e.client.PatchConfig(context.Background(), patch); err != nil {
			t.Fatalf("PatchConfig(codex group): %v", err)
		}
		for wait := time.Now().Add(3 * time.Second); time.Now().Before(wait); {
			status, body := post(t, e, http.MethodGet, "/v1/models", nil)
			if status == http.StatusOK && strings.Contains(string(body), `"codex/gpt-test"`) {
				if attempt > 0 {
					t.Logf("the Codex group was applied on write %d", attempt+1)
				}
				return up
			}
			time.Sleep(100 * time.Millisecond)
		}
		if time.Now().After(deadline) {
			t.Fatal("model codex/gpt-test not listed within 30s")
		}
	}
}

func post(t *testing.T, e engine, method, path string, body map[string]any) (int, []byte) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, e.url+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+contractKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

// mustJSON encodes a request body as the client sends it.
func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("encode body: %v", err)
	}
	return b
}

// checkDropped asserts that every key remora reports as dropped is absent from the upstream body or carries a
// different value there.
func checkDropped(t *testing.T, reported []string, sent, recorded map[string]any) {
	t.Helper()
	for _, k := range reported {
		v, ok := recorded[k]
		if !ok {
			continue
		}
		sj, _ := json.Marshal(sent[k])
		rj, _ := json.Marshal(v)
		if bytes.Equal(sj, rj) {
			t.Errorf("reported %q as dropped, but the upstream received it unchanged (%s)", k, rj)
		}
	}
}

func TestContractCodexChatDrops(t *testing.T) {
	e := startEngine(t)
	up := withCodexUpstream(t, e)
	body := map[string]any{
		"model":               "codex/gpt-test",
		"messages":            []any{map[string]any{"role": "user", "content": "hi"}},
		"temperature":         0.5,
		"top_p":               0.9,
		"max_tokens":          100,
		"parallel_tool_calls": false,
		"stop":                []any{"x"},
	}
	status, resp := post(t, e, http.MethodPost, "/v1/chat/completions", body)
	if status != http.StatusOK {
		t.Fatalf("chat completion: %d %s", status, resp)
	}
	rec := up.last(t)
	for _, k := range []string{"temperature", "top_p", "max_tokens", "max_output_tokens", "stop"} {
		if _, ok := rec[k]; ok {
			t.Errorf("upstream body has %q: %v", k, rec[k])
		}
	}
	if rec["parallel_tool_calls"] != true {
		t.Errorf("upstream parallel_tool_calls = %v, want true", rec["parallel_tool_calls"])
	}
	if rec["model"] != "gpt-test" {
		t.Errorf("upstream model = %v, want gpt-test (prefix stripped)", rec["model"])
	}
	// What x-remora-dropped-params reports for this body (plan 03, Task 7).
	reported := proxy.CodexDroppedParams("/v1/chat/completions", mustJSON(t, body))
	want := []string{"max_tokens", "parallel_tool_calls", "stop", "temperature", "top_p"}
	if !slices.Equal(reported, want) {
		t.Errorf("CodexDroppedParams = %q, want %q", reported, want)
	}
	checkDropped(t, reported, body, rec)
}

func TestContractCodexResponsesDrops(t *testing.T) {
	e := startEngine(t)
	up := withCodexUpstream(t, e)
	body := map[string]any{
		"model":               "codex/gpt-test",
		"input":               "hi",
		"temperature":         0.5,
		"top_p":               0.9,
		"max_output_tokens":   100,
		"user":                "u",
		"truncation":          "auto",
		"store":               true,
		"parallel_tool_calls": false,
	}
	status, resp := post(t, e, http.MethodPost, "/v1/responses", body)
	if status != http.StatusOK {
		t.Fatalf("responses: %d %s", status, resp)
	}
	rec := up.last(t)
	for _, k := range []string{"temperature", "top_p", "max_output_tokens", "user", "truncation"} {
		if _, ok := rec[k]; ok {
			t.Errorf("upstream body has %q: %v", k, rec[k])
		}
	}
	if rec["store"] != false {
		t.Errorf("upstream store = %v, want false", rec["store"])
	}
	if rec["parallel_tool_calls"] != true {
		t.Errorf("upstream parallel_tool_calls = %v, want true", rec["parallel_tool_calls"])
	}
	reported := proxy.CodexDroppedParams("/v1/responses", mustJSON(t, body))
	want := []string{"max_output_tokens", "parallel_tool_calls", "store", "temperature", "top_p", "truncation", "user"}
	if !slices.Equal(reported, want) {
		t.Errorf("CodexDroppedParams = %q, want %q", reported, want)
	}
	checkDropped(t, reported, body, rec)
}
