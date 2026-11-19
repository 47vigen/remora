package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

type auditTestEntry struct {
	At          time.Time      `json:"at"`
	Principal   string         `json:"principal"`
	TokenID     string         `json:"token_id"`
	TokenName   string         `json:"token_name"`
	OperationID string         `json:"operation_id"`
	Diff        map[string]any `json:"diff"`
}

func TestAuditRecordsManagementWrites(t *testing.T) {
	env := newTestEnv(t)
	env.accounts.next = Login{State: "oauth-state-fixture", URL: "https://login.example/authorize"}
	admin, bearer, err := env.server.d.Auth.CreateScopedToken(t.Context(), "automation", "admin", nil)
	if err != nil {
		t.Fatal(err)
	}
	var key, token struct{ ID, Value string }
	var applied struct{ Changes []struct{ Value string } }
	const provider = `{"kind":"openai-compatible","name":"up","base_url":"https://up.example/path-fixture?key=query-fixture","keys":[{"name":"primary","value":"provider-fixture"}]}`
	const alias = `{"name":"route","kind":"chat","capabilities":[],"members":["up/model"]}`
	apply := configBody(t, "keys:\n  - name: generated\n    allow: ['*']\n  - name: supplied\n    value: env:FIXTURE_KEY\n    allow: ['*']\n",
		map[string]string{"FIXTURE_KEY": "apply-secret-fixture"}, new(false))
	cases := []struct {
		op, method, path, body string
		status                 int
		result                 any
	}{
		{"createProvider", "POST", "/api/providers", provider, 201, nil},
		{"updateProvider", "PUT", "/api/providers/up", provider, 200, nil},
		{"testProvider", "POST", "/api/providers/up/test", "", 200, nil},
		{"startAccountLogin", "POST", "/api/accounts/logins", `{"provider":"codex"}`, 201, nil},
		{"getAccountLogin", "GET", "/api/accounts/logins/oauth-state-fixture", "", 200, nil},
		{"completeAccountLogin", "POST", "/api/accounts/logins/oauth-state-fixture/complete", `{"redirect_url":"https://callback.example/?code=callback-fixture"}`, 202, nil},
		{"cancelAccountLogin", "DELETE", "/api/accounts/logins/oauth-state-fixture", "", 204, nil},
		{"importAccount", "POST", "/api/accounts/import", `{"provider":"codex","credential":{"opaque":{"refresh":"credential-fixture"}}}`, 201, nil},
		{"updateAccount", "PATCH", "/api/accounts/100", `{"enabled":false}`, 200, nil},
		{"resetAccountCooldown", "POST", "/api/accounts/100/reset-cooldown", "", 204, nil},
		{"refreshQuota", "POST", "/api/quota/refresh", `{"account_id":"100"}`, 202, nil},
		{"setAccountStrategy", "PUT", "/api/accounts/strategies/codex", `{"strategy":"priority"}`, 200, nil},
		{"deleteAccount", "DELETE", "/api/accounts/100", "", 204, nil},
		{"compileAlias", "POST", "/api/aliases/compile", alias, 200, nil},
		{"createAlias", "POST", "/api/aliases", alias, 201, nil},
		{"updateAlias", "PUT", "/api/aliases/route", alias, 200, nil},
		{"deleteAlias", "DELETE", "/api/aliases/route", "", 204, nil},
		{"createKey", "POST", "/api/keys", `{"name":"client","allow":["*"],"value":"client-fixture"}`, 201, &key},
		{"updateKey", "PATCH", "/api/keys/{key}", `{"enabled":false}`, 200, nil},
		{"deleteKey", "DELETE", "/api/keys/{key}", "", 204, nil},
		{"createApiToken", "POST", "/api/settings/tokens", `{"name":"new-reader","scope":"read"}`, 201, &token},
		{"deleteApiToken", "DELETE", "/api/settings/tokens/{token}", "", 204, nil},
		{"exportConfig", "GET", "/api/config/export", "", 200, nil},
		{"planConfig", "POST", "/api/config/plan", apply, 200, nil},
		{"applyConfig", "POST", "/api/config/apply", apply, 200, &applied},
		{"applyConfig", "POST", "/api/config/apply", strings.TrimSuffix(apply, "}") + `,"plan_hash":"stale"}`, 412, nil},
		{"deleteProvider", "DELETE", "/api/providers/up", "", 204, nil},
		{"deleteProvider", "DELETE", "/api/providers/up", "", 404, nil},
		{"changePassword", "PUT", "/api/settings/password", `{"current":"` + testAdminPassword + `","new":"updated-password-fixture"}`, 204, nil},
		{"logout", "POST", "/api/auth/logout", "", 204, nil},
	}
	start := time.Now()
	for _, c := range cases {
		path := strings.NewReplacer("{key}", key.ID, "{token}", token.ID).Replace(c.path)
		var code int
		var body []byte
		if c.op == "changePassword" || c.op == "logout" {
			code, body = env.do(t, env.client, c.method, path, c.body)
		} else {
			code, body = env.doBearer(t, bearer, c.method, path, c.body)
		}
		if code != c.status {
			t.Fatalf("%s: got status %d, want %d", c.op, code, c.status)
		}
		if c.result != nil && json.Unmarshal(body, c.result) != nil {
			t.Fatalf("%s: cannot decode created resource", c.op)
		}
	}

	secrets := []string{bearer, token.Value, key.Value, "provider-fixture", "query-fixture", "path-fixture", "credential-fixture", "callback-fixture", "oauth-state-fixture", testAdminPassword, "updated-password-fixture", "apply-secret-fixture", "FIXTURE_KEY"}
	generated := false
	for _, change := range applied.Changes {
		if change.Value != "" {
			secrets = append(secrets, change.Value)
			generated = true
		}
	}
	if !generated {
		t.Fatal("fixture apply must generate a key")
	}
	assertSafe := func(data []byte) {
		t.Helper()
		for _, secret := range secrets {
			if secret != "" && bytes.Contains(data, []byte(secret)) {
				t.Fatal("audit contains a credential")
			}
		}
	}
	var stored []byte
	if err := env.server.d.Audit.DB().QueryRowContext(t.Context(), "SELECT group_concat(diff) FROM audit").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	assertSafe(stored)

	read := scopeToken(t, env, "read")
	get := func(q string) []auditTestEntry {
		code, body := env.doBearer(t, read, http.MethodGet, "/api/audit"+q, "")
		if code != http.StatusOK {
			t.Fatalf("GET /api/audit: status %d", code)
		}
		var page struct {
			Items []auditTestEntry
			Total int
		}
		assertSafe(body)
		decode(t, body, &page)
		if q == "" && page.Total != len(page.Items) {
			t.Error("unpaged result has an incorrect total")
		}
		if strings.Contains(q, "operation=") && (page.Total != 1 || page.Items == nil) {
			t.Error("filtered result has an incorrect total or nil items")
		}
		return page.Items
	}
	items := get("")
	want := map[string]bool{}
	for _, item := range loadSpec(t).Paths.Map() {
		for _, op := range item.Operations() {
			scope := op.Extensions["x-remora-scope"]
			if (scope == "admin" || scope == "operate") && op.OperationID != "getAccountLogin" {
				want[op.OperationID] = true
			}
		}
	}
	for _, item := range items {
		if !want[item.OperationID] {
			t.Errorf("unexpected or duplicate entry for %s", item.OperationID)
		}
		delete(want, item.OperationID)
		if item.At.Before(start) || item.At.After(time.Now()) || item.Diff == nil {
			t.Errorf("missing timestamp or diff for %s", item.OperationID)
		}
		if item.OperationID == "changePassword" {
			if item.Principal != "session" || item.TokenID != "" || item.TokenName != "" || !reflect.DeepEqual(item.Diff, map[string]any{"current": "[set]", "new": "[set]"}) {
				t.Error("password change should name the session and mask both passwords")
			}
		} else if item.Principal != "token" || item.TokenID != admin.ID || item.TokenName != admin.Name {
			t.Errorf("incorrect token attribution for %s", item.OperationID)
		}
		if item.OperationID == "createProvider" && (item.Diff["keys"] != "[set]" || item.Diff["base_url"] != "https://up.example" || item.Diff["name"] != "up") {
			t.Error("provider diff should preserve safe values and mask keys")
		}
	}
	if len(want) > 0 {
		t.Errorf("missing audit entries: %v", want)
	}
	first, second := get("?limit=1"), get("?limit=1&offset=1")
	if len(first) != 1 || len(second) != 1 || first[0].OperationID != "changePassword" || second[0].OperationID != "deleteProvider" {
		t.Error("pages should be newest-first with a stable offset")
	}
	filtered := get("?operation=updateAccount&limit=1")
	if len(filtered) != 1 || filtered[0].OperationID != "updateAccount" || !reflect.DeepEqual(filtered[0].Diff, map[string]any{"id": "100", "enabled": false}) {
		t.Error("expected only the fields supplied by the account patch and its target")
	}
	if len(get("?operation=updateAccount&offset=1")) != 0 {
		t.Error("operation filter must apply before pagination")
	}
}
