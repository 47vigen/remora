//go:build e2e

package e2e

import (
	"bytes"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/47vigen/remora/test/fakes/control"
)

// Names this test creates; nothing in testdata/remora.yaml or another test uses them.
const (
	mgmtProvider = "mgmt"
	mgmtAlias    = "e2e-fast"
	mgmtKey      = "e2e-mgmt"
	// hello holds one zero-width non-joiner (U+200C) between the words; it is compared byte for byte.
	hello = "سلام\u200cدنیا"
)

// admin is a management API client signed in with the admin session cookie.
type admin struct {
	t      *testing.T
	client *http.Client
}

// loginClient signs in with compose's admin password and keeps the session cookie in a jar.
func loginClient(t *testing.T) *admin {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	a := &admin{t: t, client: &http.Client{Jar: jar, Timeout: 30 * time.Second}}
	if code, body := a.do(http.MethodPost, "/api/auth/login", `{"password":"e2e-admin-password"}`); code != http.StatusNoContent {
		t.Fatalf("login: status %d, want 204; body %s", code, body)
	}
	if code, body := a.do(http.MethodGet, "/api/auth/session", ""); code != http.StatusOK {
		t.Fatalf("session after login: status %d, want 200; body %s", code, body)
	}
	return a
}

// do sends one management request with an optional JSON body and returns the status and the whole body.
func (a *admin) do(method, path, body string) (int, []byte) {
	a.t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(a.t.Context(), method, remoraURL(a.t, path), r)
	if err != nil {
		a.t.Fatalf("%s %s: %v", method, path, err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.client.Do(req)
	if err != nil {
		a.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		a.t.Fatalf("%s %s: read body: %v", method, path, err)
	}
	return resp.StatusCode, out
}

// must sends a request and fails the test unless it answers want.
func (a *admin) must(method, path, body string, want int) []byte {
	a.t.Helper()
	code, out := a.do(method, path, body)
	if code != want {
		a.t.Fatalf("%s %s: status %d, want %d; body %s", method, path, code, want, out)
	}
	return out
}

// cleanup deletes what this test may have created, newest first so nothing still references what goes. A miss
// (404) is fine: the test may have stopped before creating it. Running it first also clears a previous run.
func (a *admin) cleanup(keyIDs ...string) {
	a.t.Helper()
	for _, id := range keyIDs {
		a.deleteIfAny("/api/keys/" + url.PathEscape(id))
	}
	var keys struct {
		Items []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"items"`
	}
	decodeJSON(a.t, a.must(http.MethodGet, "/api/keys?limit=200", "", http.StatusOK), &keys)
	for _, k := range keys.Items {
		if k.Name == mgmtKey {
			a.deleteIfAny("/api/keys/" + url.PathEscape(k.ID))
		}
	}
	a.deleteIfAny("/api/aliases/" + url.PathEscape(mgmtAlias))
	a.deleteIfAny("/api/providers/" + url.PathEscape(mgmtProvider))
}

func (a *admin) deleteIfAny(path string) {
	a.t.Helper()
	if code, body := a.do(http.MethodDelete, path, ""); code != http.StatusNoContent && code != http.StatusNotFound {
		a.t.Errorf("DELETE %s: status %d; body %s", path, code, body)
	}
}

// mgmtLog is the part of a LogEntry this test reads.
type mgmtLog struct {
	ID       string `json:"id"`
	Key      string `json:"key"`
	Alias    string `json:"alias"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Status   string `json:"status"`
}

// logs lists log entries matching query (already encoded) and returns them with the raw body.
func (a *admin) logs(query string) ([]mgmtLog, []byte) {
	a.t.Helper()
	raw := a.must(http.MethodGet, "/api/logs?"+query, "", http.StatusOK)
	var list struct {
		Items []mgmtLog `json:"items"`
	}
	decodeJSON(a.t, raw, &list)
	return list.Items, raw
}

// eventually polls cond every 250ms until it holds or timeout passes.
func eventually(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("%s: not true after %s", what, timeout)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func TestManagementEndToEnd(t *testing.T) {
	resetFakes(t)
	a := loginClient(t)
	a.cleanup()
	var keyID string
	defer func() { a.cleanup(keyID) }()

	// Configure: a provider on the fakes, an alias over it and a key that may call only the alias.
	a.must(http.MethodPost, "/api/providers", mustJSON(t, map[string]any{
		"name": mgmtProvider, "kind": "openai-compatible", "base_url": "http://fakes:9090/u/" + mgmtProvider,
		"allow_private_network": true, "keys": []map[string]string{{"name": "main", "value": "fake-key"}},
	}), http.StatusCreated)
	a.must(http.MethodPost, "/api/aliases", mustJSON(t, map[string]any{
		"name": mgmtAlias, "kind": "chat", "members": []string{mgmtProvider + "/m1"},
	}), http.StatusCreated)
	var key struct {
		ID    string   `json:"id"`
		Name  string   `json:"name"`
		Allow []string `json:"allow"`
		Value string   `json:"value"`
	}
	decodeJSON(t, a.must(http.MethodPost, "/api/keys", mustJSON(t, map[string]any{
		"name": mgmtKey, "allow": []string{mgmtAlias},
	}), http.StatusCreated), &key)
	keyID = key.ID
	if key.ID == "" || key.Value == "" || key.Name != mgmtKey {
		t.Fatalf("created key %+v: want an id, a value and name %q", key, mgmtKey)
	}

	// Call /v1 with the new key. The engines pick up the new provider shortly after it is stored, so the first
	// call is retried until it is served.
	var rep reply
	eventually(t, 30*time.Second, "chat through the new alias", func() bool {
		rep = doV1(t, http.MethodPost, "/v1/chat/completions", key.Value, chatMessage(mgmtAlias, hello))
		return rep.Status == http.StatusOK
	})
	wantHeader(t, rep, "X-Remora-Alias", mgmtAlias)
	reqs := received(t, mgmtProvider, control.RouteOpenAIChat)
	if len(reqs) == 0 {
		t.Fatalf("the %s upstream got no chat request", mgmtProvider)
	}
	var sent struct {
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	decodeJSON(t, reqs[len(reqs)-1].Body, &sent)
	if len(sent.Messages) != 1 || !bytes.Equal([]byte(sent.Messages[0].Content), []byte(hello)) {
		t.Errorf("upstream got messages %+v, want the one message %q byte for byte", sent.Messages, hello)
	}

	// The alias's own member is a real model, but not in the key's allowlist.
	refused := doV1(t, http.MethodPost, "/v1/chat/completions", key.Value, chatMessage(mgmtProvider+"/m1", hello))
	wantStatus(t, refused, http.StatusForbidden)
	if code := errorCode(t, refused); code != "model_not_allowed" {
		t.Errorf("error.code %q, want model_not_allowed; body %s", code, refused.Body)
	}

	// The call shows in the logs, by alias and by key.
	q := url.Values{"alias": {mgmtAlias}}.Encode()
	var entries []mgmtLog
	eventually(t, 10*time.Second, "a log entry for "+mgmtAlias, func() bool {
		entries, _ = a.logs(q)
		return len(entries) >= 1
	})
	e := entries[0]
	if e.Provider != mgmtProvider || e.Key != key.ID || e.Alias != mgmtAlias || e.Model != "m1" {
		t.Errorf("log entry %+v: want provider %q, key %q, alias %q, model m1", e, mgmtProvider, key.ID, mgmtAlias)
	}
	byKey, rawList := a.logs(url.Values{"key": {key.ID}}.Encode())
	if len(byKey) == 0 {
		t.Errorf("no log entry for key %s", key.ID)
	}
	for _, k := range byKey {
		if k.Key != key.ID {
			t.Errorf("key filter returned entry %+v of another key", k)
		}
	}
	rawDetail := a.must(http.MethodGet, "/api/logs/"+url.PathEscape(e.ID), "", http.StatusOK)
	for what, raw := range map[string][]byte{"log list": rawList, "log detail": rawDetail} {
		if loc := leak.FindIndex(raw); loc != nil {
			t.Errorf("%s names an engine: %q", what, raw[max(loc[0]-80, 0):min(loc[1]+80, len(raw))])
		}
	}

	// Export, then plan the export: the store already matches it, the new provider, alias and key included.
	exported := a.must(http.MethodGet, "/api/config/export", "", http.StatusOK)
	for _, name := range []string{mgmtProvider, mgmtAlias, mgmtKey} {
		if !bytes.Contains(exported, []byte(name)) {
			t.Errorf("export does not name %q:\n%s", name, exported)
		}
	}
	var plan struct {
		Changes []map[string]any `json:"changes"`
	}
	decodeJSON(t, a.must(http.MethodPost, "/api/config/plan", mustJSON(t, map[string]any{"document": string(exported)}), http.StatusOK), &plan)
	if len(plan.Changes) != 0 {
		t.Errorf("planning the export wants changes %v, want none; export:\n%s", plan.Changes, exported)
	}
}

// chatMessage is a non-streaming chat body with one user message.
func chatMessage(model, content string) map[string]any {
	return map[string]any{"model": model, "messages": []map[string]any{{"role": "user", "content": content}}}
}
