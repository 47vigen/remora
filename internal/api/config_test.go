package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/47vigen/remora/internal/alias"
	"github.com/47vigen/remora/internal/api/gen"
	"github.com/47vigen/remora/internal/config"
)

// populate gives the fakes a provider, two aliases and a client key.
func (e *testEnv) populate(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	if _, err := e.providers.Create(ctx, Provider{
		Name: "up-a", Kind: "openai-compatible", BaseURL: "http://upstream.test/a",
		Keys: []ProviderKey{{Name: "main", Value: "sk-upstream-secret"}},
	}); err != nil {
		t.Fatal(err)
	}
	for _, a := range []alias.Alias{
		{Name: "inner", Kind: alias.KindChat, Members: []alias.Member{{Ref: "up-a/m1"}}},
		{Name: "fast", Kind: alias.KindChat, Members: []alias.Member{{Ref: "inner"}, {Ref: "up-a/m2"}}},
	} {
		if _, err := e.aliases.Put(ctx, a, true); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := e.keys.Create(ctx, "full", []string{"*"}, "sk-client-secret-value"); err != nil {
		t.Fatal(err)
	}
}

func configBody(t *testing.T, doc string, secrets map[string]string, prune *bool) string {
	t.Helper()
	req := gen.ConfigRequest{Document: doc, Prune: prune}
	if secrets != nil {
		req.Secrets = &secrets
	}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func decodeChanges(t *testing.T, body []byte) []gen.Change {
	t.Helper()
	var cs gen.ConfigChanges
	if err := json.Unmarshal(body, &cs); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return cs.Changes
}

func TestConfigEndpoints(t *testing.T) {
	env := newTestEnv(t)
	env.populate(t)

	// Export: the YAML document as an attachment, without secrets.
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, env.srv.URL+"/api/config/export", http.NoBody)
	resp, err := env.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	exported, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "application/yaml" ||
		resp.Header.Get("Content-Disposition") != "attachment; filename=remora.yaml" {
		t.Fatalf("export = %d %v\n%s", resp.StatusCode, resp.Header, exported)
	}
	doc := string(exported)
	if strings.Contains(doc, "sk-upstream-secret") || strings.Contains(doc, "sk-client-secret-value") {
		t.Errorf("export shows a secret:\n%s", doc)
	}
	if _, err := config.Parse([]byte(doc)); err != nil {
		t.Fatalf("exported document does not parse: %v\n%s", err, doc)
	}

	// Plan of the export: no changes.
	code, body := env.do(t, env.client, http.MethodPost, "/api/config/plan", configBody(t, doc, nil, nil))
	if code != http.StatusOK || strings.TrimSpace(string(body)) != `{"changes":[]}` {
		t.Fatalf("plan(export) = %d %s", code, body)
	}

	// Invalid documents: 400 naming the problem's path.
	for _, bad := range []struct{ doc, want string }{
		{"providers: [\n", "remora.yaml"},
		{"version: 2\n", "version"},
		{"aliases:\n  - {name: v, kind: video, members: [up-a/m1]}\n", "aliases[0].kind"},
	} {
		for _, path := range []string{"/api/config/plan", "/api/config/apply"} {
			code, body := env.do(t, env.client, http.MethodPost, path, configBody(t, bad.doc, nil, nil))
			if code != http.StatusBadRequest || errCode(t, body) != "invalid_request" || !strings.Contains(string(body), bad.want) {
				t.Errorf("%s %q = %d %s, want 400 invalid_request mentioning %q", path, bad.doc, code, body, bad.want)
			}
		}
	}

	// Plan, then apply, an added alias; plan changes nothing.
	added := strings.Replace(doc, "aliases:\n", "aliases:\n  - name: x\n    kind: chat\n    members: [fast]\n", 1)
	code, body = env.do(t, env.client, http.MethodPost, "/api/config/plan", configBody(t, added, nil, nil))
	want := []gen.Change{{Op: gen.Create, Kind: gen.ChangeKindAlias, Name: "x", Fields: []string{}}}
	if code != http.StatusOK || !reflect.DeepEqual(decodeChanges(t, body), want) {
		t.Fatalf("plan = %d %s", code, body)
	}
	if _, err := env.aliases.Get(context.Background(), "x"); err == nil {
		t.Fatal("plan created the alias")
	}
	code, body = env.do(t, env.client, http.MethodPost, "/api/config/apply", configBody(t, added, nil, nil))
	if code != http.StatusOK || !reflect.DeepEqual(decodeChanges(t, body), want) {
		t.Fatalf("apply = %d %s", code, body)
	}
	if _, err := env.aliases.Get(context.Background(), "x"); err != nil {
		t.Fatalf("apply did not create the alias: %v", err)
	}

	// prune defaults to true; false keeps what the document leaves out.
	off := false
	code, body = env.do(t, env.client, http.MethodPost, "/api/config/plan", configBody(t, "version: 1\n", nil, &off))
	if code != http.StatusOK || len(decodeChanges(t, body)) != 0 {
		t.Errorf("plan(empty, prune false) = %d %s", code, body)
	}
	code, body = env.do(t, env.client, http.MethodPost, "/api/config/plan", configBody(t, "version: 1\n", nil, nil))
	if code != http.StatusOK || len(decodeChanges(t, body)) != 5 { // key full, aliases x, fast, inner, provider up-a
		t.Errorf("plan(empty) = %d %s", code, body)
	}

	// A new provider whose secret is not sent: 400 naming the variable.
	newProvider := "providers:\n  - {name: new, kind: soniox, keys: [{name: main, value: env:NEW_KEY}]}\n"
	code, body = env.do(t, env.client, http.MethodPost, "/api/config/plan", configBody(t, newProvider, nil, &off))
	if code != http.StatusBadRequest || !strings.Contains(string(body), "env:NEW_KEY") {
		t.Errorf("unset secret = %d %s", code, body)
	}

	// A client key created without a value: its generated value is answered once.
	code, body = env.do(t, env.client, http.MethodPost, "/api/config/apply", configBody(t, "keys:\n  - {name: gen, allow: ['*']}\n", nil, &off))
	cs := decodeChanges(t, body)
	if code != http.StatusOK || len(cs) != 1 || cs[0].Value == nil || !strings.HasPrefix(*cs[0].Value, "sk-") {
		t.Errorf("apply new key = %d %s", code, body)
	}
}

func TestApplyConfigReportsPartialFailure(t *testing.T) {
	env := newTestEnv(t)
	env.aliases.putErr = fmt.Errorf("alias: %w: nowhere", alias.ErrUnknownMember)
	doc := "providers:\n  - {name: p, kind: soniox, keys: [{name: main, value: env:P_KEY}]}\n" +
		"aliases:\n  - {name: a, kind: chat, members: [p/m]}\n"
	code, body := env.do(t, env.client, http.MethodPost, "/api/config/apply", configBody(t, doc, map[string]string{"P_KEY": "sk-p"}, nil))
	if code != http.StatusBadRequest || !strings.Contains(string(body), "create alias a") || !strings.Contains(string(body), "+ provider p") {
		t.Errorf("apply = %d %s; want 400 naming the failed change and the applied one", code, body)
	}
	if _, err := env.providers.Get(context.Background(), "p"); err != nil {
		t.Error("the change before the failure was not kept")
	}
}

// pendingOperations are the operations plan 05 has not implemented yet (Tasks 8–11: logs, stats and key usage).
// Remove each one as its task lands.
var pendingOperations = map[string]bool{
	"GetKeyUsage": true, "ListLogs": true, "GetLog": true, "GetStats": true,
}

// TestNoUnimplementedLeft calls every operation of the generated interface over HTTP, with path parameters set and
// an empty JSON object as the body, and expects an answer other than 501.
func TestNoUnimplementedLeft(t *testing.T) {
	env := newTestEnv(t)
	env.populate(t)
	spec := loadSpec(t)
	type route struct{ method, path string }
	routes := map[string]route{}
	for path, item := range spec.Paths.Map() {
		for method, op := range item.Operations() {
			routes[strings.ToUpper(op.OperationID[:1])+op.OperationID[1:]] = route{method, path}
		}
	}

	iface := reflect.TypeFor[gen.StrictServerInterface]()
	var last *route // logout ends the session, so it goes last
	for i := range iface.NumMethod() {
		name := iface.Method(i).Name
		r, ok := routes[name]
		if !ok {
			t.Errorf("%s: no operation in the contract", name)
			continue
		}
		if pendingOperations[name] {
			continue
		}
		if name == "Logout" {
			last = &r
			continue
		}
		if code := callRoute(t, env, r.method, r.path); code == http.StatusNotImplemented {
			t.Errorf("%s (%s %s) answers 501", name, r.method, r.path)
		}
	}
	if last == nil {
		t.Fatal("no Logout operation")
	}
	if code := callRoute(t, env, last.method, last.path); code == http.StatusNotImplemented {
		t.Errorf("Logout answers 501")
	}
}

func callRoute(t *testing.T, env *testEnv, method, path string) int {
	t.Helper()
	for _, p := range []string{"{name}", "{id}", "{state}"} {
		path = strings.ReplaceAll(path, p, "x")
	}
	body := ""
	if method == http.MethodPost || method == http.MethodPut || method == http.MethodPatch {
		body = "{}"
	}
	code, _ := env.do(t, env.client, method, path, body)
	return code
}
