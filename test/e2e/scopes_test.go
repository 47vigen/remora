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

func scopedClient(t *testing.T, a *admin, name, scope string) (*admin, string) {
	t.Helper()
	status, body := a.do(http.MethodPost, "/api/settings/tokens", mustJSON(t, map[string]string{"name": name, "scope": scope}))
	if status != http.StatusCreated {
		t.Fatalf("create %s token: status %d, want 201", scope, status)
	}
	var token struct{ ID, Value string }
	if err := json.Unmarshal(body, &token); err != nil || token.ID == "" || token.Value == "" {
		t.Fatalf("create %s token: expected a token ID and value", scope)
	}
	t.Cleanup(func() { a.deleteIfAny("/api/settings/tokens/" + token.ID) })
	return &admin{t: t, client: &http.Client{Transport: bearer{token.Value}, Timeout: 30 * time.Second}}, token.ID
}

func TestReadScopeCannotDeleteProvider(t *testing.T) {
	const name = "e2e-scope-provider"
	path := "/api/providers/" + name
	a := loginClient(t)
	t.Cleanup(func() { a.deleteIfAny(path) })
	a.must(http.MethodPost, "/api/providers", mustJSON(t, map[string]any{
		"name": name, "kind": "openai-compatible", "base_url": "http://fakes:9090/u/" + name,
		"allow_private_network": true, "keys": []map[string]string{{"name": "main", "value": "fake-key"}},
	}), http.StatusCreated)
	reader, _ := scopedClient(t, a, "e2e-scope-read", "read")
	raw := reader.must(http.MethodDelete, path, "", http.StatusForbidden)
	if code := errorCode(t, reply{Body: raw}); code != "forbidden" {
		t.Errorf("DELETE provider error code = %q, want forbidden", code)
	}

	cs, err := sdk.NewClient(&sdk.Implementation{Name: "e2e", Version: "0"}, nil).Connect(t.Context(), &sdk.StreamableClientTransport{
		Endpoint: remoraURL(t, "/mcp"), DisableStandaloneSSE: true, HTTPClient: reader.client,
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	res, err := cs.CallTool(t.Context(), &sdk.CallToolParams{
		Name: "remora_delete", Arguments: map[string]any{"kind": "provider", "id": name},
	})
	if err != nil {
		t.Fatalf("remora_delete: %v", err)
	}
	if !res.IsError || !slices.ContainsFunc(res.Content, func(c sdk.Content) bool {
		text, ok := c.(*sdk.TextContent)
		return ok && strings.Contains(text.Text, "HTTP 403")
	}) {
		t.Error("remora_delete: want a tool error with HTTP 403")
	}
	a.must(http.MethodGet, path, "", http.StatusOK)
}

func TestOperateScopeCanDisableAccount(t *testing.T) {
	a := loginClient(t)
	id := importQuotaAccount(t, a, "codex", "codex")
	operator, _ := scopedClient(t, a, "e2e-scope-operate", "operate")
	var account struct {
		ID      string `json:"id"`
		Enabled *bool  `json:"enabled"`
	}
	decodeJSON(t, operator.must(http.MethodPatch, "/api/accounts/"+id, `{"enabled":false}`, http.StatusOK), &account)
	if account.ID != id || account.Enabled == nil || *account.Enabled {
		t.Errorf("account update did not return the disabled account %q", id)
	}
}

func TestAuditRecordsAdminTokenWrite(t *testing.T) {
	const principalName, aliasName = "e2e-audit-admin", "e2e-audit-alias"
	a := loginClient(t)
	writer, tokenID := scopedClient(t, a, principalName, "admin")
	t.Cleanup(func() { a.deleteIfAny("/api/aliases/" + aliasName) })
	writer.must(http.MethodPost, "/api/aliases", mustJSON(t, map[string]any{
		"name": aliasName, "kind": "chat", "members": []string{"up-a/m1"},
	}), http.StatusCreated)

	var page struct {
		Items []struct {
			Principal   string `json:"principal"`
			TokenID     string `json:"token_id"`
			TokenName   string `json:"token_name"`
			OperationID string `json:"operation_id"`
			Diff        struct {
				Name string `json:"name"`
			} `json:"diff"`
		} `json:"items"`
	}
	decodeJSON(t, a.must(http.MethodGet, "/api/audit?operation=createAlias&limit=1", "", http.StatusOK), &page)
	if len(page.Items) != 1 {
		t.Fatalf("audit page: got %d items, want 1", len(page.Items))
	}
	entry := page.Items[0]
	if entry.Principal != "token" || entry.TokenID != tokenID || entry.TokenName != principalName ||
		entry.OperationID != "createAlias" || entry.Diff.Name != aliasName {
		t.Error("audit entry did not attribute the alias creation to the admin token")
	}
}
