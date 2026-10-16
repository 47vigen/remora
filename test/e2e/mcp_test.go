//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// mcpTools are the eleven tools /mcp serves.
var mcpTools = []string{
	"remora_account", "remora_alias_compile", "remora_config_apply", "remora_config_export", "remora_config_plan",
	"remora_delete", "remora_inspect", "remora_logs", "remora_overview", "remora_provider_test", "remora_stats",
}

type bearer struct{ token string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

// mcpCall calls a tool and decodes its structuredContent; a tool error fails the test.
func mcpCall(t *testing.T, cs *sdk.ClientSession, name string, args map[string]any) map[string]any {
	t.Helper()
	res, err := cs.CallTool(t.Context(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	b, _ := json.Marshal(res.StructuredContent)
	if res.IsError {
		t.Fatalf("%s: tool error %v", name, res.Content)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("%s: structuredContent %s: %v", name, b, err)
	}
	return out
}

func TestMCP(t *testing.T) {
	const name = "e2e-mcp"
	a := loginClient(t)
	var tok struct{ ID, Value string }
	if err := json.Unmarshal(a.must(http.MethodPost, "/api/settings/tokens", `{"name":"`+name+`"}`, http.StatusCreated), &tok); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.do(http.MethodDelete, "/api/settings/tokens/"+tok.ID, "") })
	t.Cleanup(func() { a.do(http.MethodDelete, "/api/aliases/"+name, "") })

	req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, remoraURL(t, "/mcp"), strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("/mcp without a token = %d, want 401", resp.StatusCode)
	}

	cs, err := sdk.NewClient(&sdk.Implementation{Name: "e2e", Version: "0"}, nil).Connect(t.Context(), &sdk.StreamableClientTransport{
		Endpoint: remoraURL(t, "/mcp"), DisableStandaloneSSE: true,
		HTTPClient: &http.Client{Transport: bearer{tok.Value}, Timeout: 30 * time.Second},
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })

	tools, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	if !slices.Equal(names, mcpTools) {
		t.Errorf("tools = %v, want %v", names, mcpTools)
	}

	if o := mcpCall(t, cs, "remora_overview", map[string]any{}); o["status"] != "ok" || o["providers"].(float64) < 1 {
		t.Errorf("overview = %v", o)
	}

	doc := "aliases:\n  - name: " + name + "\n    kind: chat\n    members: [up-a/m1]\n" // fast's first member
	plan := mcpCall(t, cs, "remora_config_plan", map[string]any{"document": doc})
	changes, _ := plan["changes"].([]any)
	if len(changes) != 1 || changes[0].(map[string]any)["op"] != "create" || changes[0].(map[string]any)["name"] != name || plan["plan_hash"] == "" {
		t.Fatalf("plan = %v", plan)
	}
	applied := mcpCall(t, cs, "remora_config_apply", map[string]any{"document": doc, "plan_hash": plan["plan_hash"]})
	if applied["applied"] != true {
		t.Fatalf("apply = %v", applied)
	}
	a.must(http.MethodGet, "/api/aliases/"+name, "", http.StatusOK)
	for _, kept := range []string{"fast", "inner", "emb", "jev", "cool"} {
		a.must(http.MethodGet, "/api/aliases/"+kept, "", http.StatusOK) // prune false deleted nothing
	}

	stale := mcpCall(t, cs, "remora_config_apply", map[string]any{"document": doc, "plan_hash": plan["plan_hash"]})
	if stale["applied"] != false || len(stale["changes"].([]any)) != 0 || stale["plan_hash"] == plan["plan_hash"] {
		t.Errorf("stale apply = %v", stale)
	}

	shown := mcpCall(t, cs, "remora_inspect", map[string]any{"kind": "aliases", "id": name})
	if targets, _ := shown["chain"].(map[string]any)["targets"].([]any); len(targets) != 1 {
		t.Errorf("inspect = %v", shown)
	}

	if d := mcpCall(t, cs, "remora_delete", map[string]any{"kind": "alias", "id": name}); d["deleted"] != "alias "+name {
		t.Errorf("delete = %v", d)
	}
	a.must(http.MethodGet, "/api/aliases/"+name, "", http.StatusNotFound)
}
