package api

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
)

// doBearer sends a request with an API token and no cookies.
func (e *testEnv) doBearer(t *testing.T, token, method, path, body string) (int, []byte) {
	t.Helper()
	var rd io.Reader = http.NoBody
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, e.srv.URL+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, b
}

func (e *testEnv) login(t *testing.T, password string) int {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	code, _ := e.do(t, &http.Client{Jar: jar}, http.MethodPost, "/api/auth/login", `{"password":"`+password+`"}`)
	return code
}

func TestChangePassword(t *testing.T) {
	env := newTestEnv(t)
	const next = "a-new-admin-pw" // 14 characters

	code, body := env.do(t, env.client, http.MethodPut, "/api/settings/password", `{"current":"wrong-password-1","new":"`+next+`"}`)
	if code != http.StatusUnauthorized || errCode(t, body) != "unauthorized" {
		t.Errorf("wrong current = %d %s", code, body)
	}
	code, body = env.do(t, env.client, http.MethodPut, "/api/settings/password", `{"current":"`+testAdminPassword+`","new":"elevenchars"}`)
	if code != http.StatusBadRequest || errCode(t, body) != "invalid_request" {
		t.Errorf("11-character password = %d %s", code, body)
	}
	code, body = env.do(t, env.client, http.MethodPut, "/api/settings/password", `{"current":"`+testAdminPassword+`","new":"twelve-chars"}`)
	if code != http.StatusNoContent {
		t.Fatalf("12-character password = %d %s", code, body)
	}

	if code := env.login(t, testAdminPassword); code != http.StatusUnauthorized {
		t.Errorf("login with the old password = %d, want 401", code)
	}
	if code := env.login(t, "twelve-chars"); code != http.StatusNoContent {
		t.Errorf("login with the new password = %d, want 204", code)
	}
	if code, body := env.do(t, env.client, http.MethodGet, "/api/auth/session", ""); code != http.StatusOK {
		t.Errorf("this session after the change = %d %s, want still valid", code, body)
	}
}

func TestApiTokenLifecycle(t *testing.T) {
	env := newTestEnv(t)
	code, body := env.do(t, env.client, http.MethodPost, "/api/settings/tokens", `{"name":"ci"}`)
	if code != http.StatusCreated {
		t.Fatalf("create = %d %s", code, body)
	}
	var created struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Prefix string `json:"prefix"`
		Value  string `json:"value"`
	}
	decode(t, body, &created)
	if created.ID == "" || created.Name != "ci" || !strings.HasPrefix(created.Value, "rmt_") ||
		created.Prefix == "" || !strings.HasPrefix(created.Value, created.Prefix) {
		t.Fatalf("created = %s", body)
	}

	code, body = env.do(t, env.client, http.MethodGet, "/api/settings/tokens", "")
	if code != http.StatusOK || strings.Contains(string(body), `"value"`) || strings.Contains(string(body), created.Value) {
		t.Errorf("list = %d %s", code, body)
	}
	if !strings.Contains(string(body), `"prefix":"`+created.Prefix+`"`) || !strings.Contains(string(body), `"total":1`) {
		t.Errorf("list = %s, want the prefix and total 1", body)
	}

	if code, body := env.doBearer(t, created.Value, http.MethodGet, "/api/providers", ""); code != http.StatusOK {
		t.Errorf("token as bearer = %d %s", code, body)
	}
	code, body = env.do(t, env.client, http.MethodGet, "/api/settings/tokens", "")
	if code != http.StatusOK || !strings.Contains(string(body), `"last_used_at"`) {
		t.Errorf("list after use = %d %s, want last_used_at", code, body)
	}

	if code, body := env.do(t, env.client, http.MethodDelete, "/api/settings/tokens/"+created.ID, ""); code != http.StatusNoContent {
		t.Fatalf("delete = %d %s", code, body)
	}
	if code, body := env.doBearer(t, created.Value, http.MethodGet, "/api/providers", ""); code != http.StatusUnauthorized {
		t.Errorf("deleted token = %d %s, want 401", code, body)
	}
	if code, body := env.do(t, env.client, http.MethodDelete, "/api/settings/tokens/"+created.ID, ""); code != http.StatusNotFound {
		t.Errorf("delete again = %d %s", code, body)
	}
	if code, body := env.do(t, env.client, http.MethodPost, "/api/settings/tokens", `{"name":""}`); code != http.StatusBadRequest {
		t.Errorf("create without a name = %d %s", code, body)
	}
}

func TestTokenCannotChangePassword(t *testing.T) {
	env := newTestEnv(t)
	code, body := env.do(t, env.client, http.MethodPost, "/api/settings/tokens", `{"name":"ci"}`)
	if code != http.StatusCreated {
		t.Fatalf("create = %d %s", code, body)
	}
	var created struct{ Value string }
	decode(t, body, &created)

	code, body = env.doBearer(t, created.Value, http.MethodPut, "/api/settings/password",
		`{"current":"`+testAdminPassword+`","new":"another-password"}`)
	if code != http.StatusForbidden || errCode(t, body) != "forbidden" {
		t.Errorf("token changing the password = %d %s, want 403 forbidden", code, body)
	}
	if code := env.login(t, testAdminPassword); code != http.StatusNoContent {
		t.Errorf("the password changed anyway: login = %d", code)
	}
}
