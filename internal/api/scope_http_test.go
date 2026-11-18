package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/47vigen/remora/internal/api/gen"
	"github.com/47vigen/remora/internal/mcp"
)

func scopeToken(t *testing.T, env *testEnv, scope string) string {
	t.Helper()
	_, value, err := env.server.d.Auth.CreateScopedToken(context.Background(), scope, scope, nil)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestScopeEnforcement(t *testing.T) {
	env := newTestEnv(t)
	paths := loadSpec(t).Paths.Map()
	params := strings.NewReplacer("{id}", "1", "{name}", "absent", "{state}", "absent", "{provider}", "absent")
	for _, tc := range []struct {
		scope, allowed string
	}{
		{"read", "none read"},
		{"operate", "none read operate"},
		{"admin", "none read operate admin"},
	} {
		t.Run(tc.scope, func(t *testing.T) {
			token := scopeToken(t, env, tc.scope)
			for path, item := range paths {
				for method, op := range item.Operations() {
					t.Run(op.OperationID, func(t *testing.T) {
						url := params.Replace(path)
						var body string
						if op.RequestBody != nil {
							body = `{}`
						}
						if op.OperationID == "login" {
							body = `{"password":"` + testAdminPassword + `"}`
						}
						code, response := env.doBearer(t, token, method, url, body)
						need := op.Extensions["x-remora-scope"].(string)
						allowed := slices.Contains(strings.Fields(tc.allowed), need) && op.OperationID != "changePassword"
						if allowed {
							// Resource and validation errors are fine once the scope check lets the handler run.
							if code == http.StatusForbidden || code == http.StatusUnauthorized {
								t.Errorf("%s %s = %d, want no authorization error", method, url, code)
							}
						} else if code != http.StatusForbidden || errCode(t, response) != "forbidden" {
							t.Errorf("%s %s = %d, want 403 forbidden", method, url, code)
						}
					})
				}
			}
		})
	}
}

func TestScopeStreamLogs(t *testing.T) {
	env := newLiveEnv(t, 0, 0)
	for _, scope := range []string{"read", "operate", "admin"} {
		t.Run(scope, func(t *testing.T) {
			conn, code, err := dialWS(env.wsURL(""), &websocket.DialOptions{
				HTTPHeader: http.Header{"Authorization": {"Bearer " + scopeToken(t, env.testEnv, scope)}},
			})
			if err != nil {
				t.Fatalf("dial: %v (%d)", err, code)
			}
			_ = conn.CloseNow()
		})
	}
}

func TestScopeTokenRoundTrip(t *testing.T) {
	env := newTestEnv(t)
	const path = "/api/settings/tokens"
	expiry := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	status, body := env.do(t, env.client, http.MethodPost, path,
		`{"name":"scoped","scope":"read","expires_at":"`+expiry.Format(time.RFC3339)+`"}`)
	if status != http.StatusCreated {
		t.Fatalf("create = %d, want 201", status)
	}
	var created gen.ApiTokenCreated
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatal("decode token creation response")
	}
	if deref(created.Scope) != "read" || created.ExpiresAt == nil || !created.ExpiresAt.Equal(expiry) {
		t.Error("create did not return the requested scope and expiry")
	}
	if status, _ := env.doBearer(t, created.Value, http.MethodDelete, path+"/"+created.Id, ""); status != http.StatusForbidden {
		t.Errorf("read token revoking itself = %d, want 403", status)
	}
	status, body = env.do(t, env.client, http.MethodGet, path, "")
	if status != http.StatusOK {
		t.Fatalf("list = %d, want 200", status)
	}
	var list gen.ApiTokenList
	decode(t, body, &list)
	if len(list.Items) != 1 || deref(list.Items[0].Scope) != "read" || list.Items[0].ExpiresAt == nil || !list.Items[0].ExpiresAt.Equal(expiry) {
		t.Error("list did not return the stored scope and expiry")
	}
}

func TestScopeMCP(t *testing.T) {
	env := newTestEnv(t)
	client, err := mcp.InProcess(env.server.Handler())
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/mcp", mcp.Handler(mcp.New(client), env.server.d.Auth))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	transport := &sdk.StreamableClientTransport{
		Endpoint: srv.URL + "/mcp", DisableStandaloneSSE: true,
		HTTPClient: &http.Client{Transport: scopeBearerTransport(scopeToken(t, env, "read"))},
	}
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "0"}, nil).Connect(context.Background(), transport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"remora_delete", map[string]any{"kind": "provider", "id": "absent"}},
		{"remora_config_apply", map[string]any{"document": "aliases: []\n", "plan_hash": "test"}},
		{"remora_overview", map[string]any{}},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: tc.tool, Arguments: tc.args})
			if err != nil {
				t.Fatal(err)
			}
			if tc.tool == "remora_overview" {
				if res.IsError || res.StructuredContent == nil {
					t.Error("overview did not return a successful result")
				}
				return
			}
			for _, c := range res.Content {
				if msg, ok := c.(*sdk.TextContent); res.IsError && ok && strings.Contains(msg.Text, "HTTP 403") {
					return
				}
			}
			t.Error("want a tool error from HTTP 403")
		})
	}
}

type scopeBearerTransport string

func (b scopeBearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+string(b))
	return http.DefaultTransport.RoundTrip(r)
}

func TestScopeInvalidTokenRequest(t *testing.T) {
	env := newTestEnv(t)
	for _, scope := range []string{"", "unknown"} {
		status, body := env.do(t, env.client, http.MethodPost, "/api/settings/tokens", `{"name":"invalid","scope":"`+scope+`"}`)
		if status != http.StatusBadRequest || errCode(t, body) != "invalid_request" {
			t.Errorf("create with scope %q = %d, want 400 invalid_request", scope, status)
		}
	}
}
