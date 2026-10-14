package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/47vigen/remora/internal/alias"
	"github.com/47vigen/remora/internal/auth"
	"github.com/47vigen/remora/internal/store"
)

const testAdminPassword = "test-admin-password"

type testEnv struct {
	srv       *httptest.Server
	client    *http.Client // logged in
	system    *fakeSystem
	providers *fakeProviders
	aliases   *fakeAliases
	keys      *fakeKeys
	accounts  *fakeAccounts
	names     *fakeNames
	logs      *fakeLogs
	server    *Server
}

// newTestEnv starts the management server over in-memory fakes and a real auth service, and logs a client in.
func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "remora.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := auth.EnsureAdmin(ctx, st, testAdminPassword); err != nil {
		t.Fatalf("EnsureAdmin: %v", err)
	}
	env := &testEnv{
		system: &fakeSystem{}, providers: &fakeProviders{}, aliases: &fakeAliases{}, keys: &fakeKeys{},
		accounts: &fakeAccounts{}, names: &fakeNames{}, logs: &fakeLogs{},
	}
	s := New(Deps{
		Providers: env.providers,
		Accounts:  env.accounts,
		Aliases:   env.aliases,
		Keys:      env.keys,
		System:    env.system,
		Names:     env.names,
		Logs:      env.logs,
		Auth:      auth.NewService(st, nil),
	})
	env.server = s
	env.srv = httptest.NewServer(s.Handler())
	t.Cleanup(env.srv.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	env.client = &http.Client{Jar: jar}
	if code, body := env.do(t, env.client, http.MethodPost, "/api/auth/login", `{"password":"`+testAdminPassword+`"}`); code != http.StatusNoContent {
		t.Fatalf("login: %d %s", code, body)
	}
	return env
}

func newTestServer(t *testing.T) (*httptest.Server, *http.Client) {
	t.Helper()
	env := newTestEnv(t)
	return env.srv, env.client
}

func (e *testEnv) do(t *testing.T, c *http.Client, method, path, body string) (int, []byte) {
	t.Helper()
	var rd io.Reader = http.NoBody
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, e.srv.URL+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.Do(req)
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

func errCode(t *testing.T, body []byte) string {
	t.Helper()
	var e struct {
		Error struct {
			Message, Type, Code string
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &e); err != nil {
		t.Fatalf("decode error body %q: %v", body, err)
	}
	if e.Error.Message == "" || e.Error.Type == "" {
		t.Errorf("error body %s lacks message or type", body)
	}
	return e.Error.Code
}

func TestHealthIsPublic(t *testing.T) {
	env := newTestEnv(t)
	code, body := env.do(t, http.DefaultClient, http.MethodGet, "/api/health", "")
	if code != http.StatusOK || strings.TrimSpace(string(body)) != `{"engines":{"front-door":"ok"},"status":"ok"}` {
		t.Errorf("health = %d %s", code, body)
	}
	env.system.setErr(errors.New("down"))
	code, body = env.do(t, http.DefaultClient, http.MethodGet, "/api/health", "")
	if code != http.StatusOK || strings.TrimSpace(string(body)) != `{"engines":{"front-door":"error"},"status":"degraded"}` {
		t.Errorf("degraded health = %d %s", code, body)
	}
}

func TestVersion(t *testing.T) {
	env := newTestEnv(t)
	want := `{"engines":[{"name":"accounts-engine","version":"v2"},{"name":"front-door","version":"v1"}],"remora":"v0.0.0-test"}`
	code, body := env.do(t, env.client, http.MethodGet, "/api/system/version", "")
	if code != http.StatusOK || strings.TrimSpace(string(body)) != want {
		t.Errorf("version = %d %s", code, body)
	}
	// Plan 01's contract: the version answers without auth too (the foundation e2e reads it anonymously).
	if code, body := env.do(t, http.DefaultClient, http.MethodGet, "/api/system/version", ""); code != http.StatusOK {
		t.Errorf("anonymous version = %d %s", code, body)
	}
}

func TestLoginSetsCookie(t *testing.T) {
	env := newTestEnv(t)
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}

	code, body := env.do(t, c, http.MethodPost, "/api/auth/login", `{"password":"wrong-password-1"}`)
	if code != http.StatusUnauthorized || errCode(t, body) != "unauthorized" {
		t.Errorf("bad login = %d %s", code, body)
	}
	if code, body := env.do(t, c, http.MethodPost, "/api/auth/login", `{"password":`); code != http.StatusBadRequest || errCode(t, body) != "invalid_request" {
		t.Errorf("malformed login = %d %s", code, body)
	}

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, env.srv.URL+"/api/auth/login",
		strings.NewReader(`{"password":"`+testAdminPassword+`"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("login = %d", resp.StatusCode)
	}
	var cookie *http.Cookie
	for _, ck := range resp.Cookies() {
		if ck.Name == auth.CookieName {
			cookie = ck
		}
	}
	if cookie == nil || cookie.Value == "" || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("session cookie = %+v", cookie)
	}

	code, body = env.do(t, c, http.MethodGet, "/api/auth/session", "")
	if code != http.StatusOK || strings.TrimSpace(string(body)) != `{"authenticated":true,"via":"session"}` {
		t.Errorf("session = %d %s", code, body)
	}
}

func TestLoginTooManyAttempts(t *testing.T) {
	env := newTestEnv(t)
	for range 5 {
		env.do(t, http.DefaultClient, http.MethodPost, "/api/auth/login", `{"password":"wrong-password-1"}`)
	}
	code, body := env.do(t, http.DefaultClient, http.MethodPost, "/api/auth/login", `{"password":"`+testAdminPassword+`"}`)
	if code != http.StatusTooManyRequests || errCode(t, body) != "too_many_attempts" {
		t.Errorf("6th login = %d %s", code, body)
	}
}

func TestLogoutClearsSession(t *testing.T) {
	env := newTestEnv(t)
	if code, body := env.do(t, env.client, http.MethodPost, "/api/auth/logout", ""); code != http.StatusNoContent {
		t.Fatalf("logout = %d %s", code, body)
	}
	if code, _ := env.do(t, env.client, http.MethodGet, "/api/auth/session", ""); code != http.StatusUnauthorized {
		t.Errorf("session after logout = %d, want 401", code)
	}
}

func TestProtectedNeedsAuth(t *testing.T) {
	env := newTestEnv(t)
	code, body := env.do(t, http.DefaultClient, http.MethodGet, "/api/providers", "")
	if code != http.StatusUnauthorized || errCode(t, body) != "unauthorized" {
		t.Errorf("no auth = %d %s", code, body)
	}
	code, body = env.do(t, env.client, http.MethodGet, "/api/nope", "")
	if code != http.StatusNotFound || errCode(t, body) != "not_found" {
		t.Errorf("unknown path = %d %s", code, body)
	}
}

func TestBearerToken(t *testing.T) {
	srv, _ := newTestServer(t)
	// A /v1 client key is not an API token.
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/api/auth/session", http.NoBody)
	req.Header.Set("Authorization", "Bearer sk-anything")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("client key on /api = %d, want 401", resp.StatusCode)
	}
}

func TestErrorMapping(t *testing.T) {
	for _, tc := range []struct {
		err      error
		status   int
		code     string
		contains string
	}{
		{fmt.Errorf("x: %w", store.ErrNotFound), http.StatusNotFound, "not_found", ""},
		{fmt.Errorf("x: %w", store.ErrDuplicate), http.StatusConflict, "conflict", ""},
		{fmt.Errorf("reconcile: change: %w", fmt.Errorf("%w: a -> b -> a", alias.ErrCycle)), http.StatusBadRequest, "invalid_request", "cycle"},
		{fmt.Errorf("%w: %q", alias.ErrUnknownMember, "x"), http.StatusBadRequest, "invalid_request", "unknown member"},
		{auth.ErrWeakPassword, http.StatusBadRequest, "invalid_request", ""},
		{badRequest("name is required"), http.StatusBadRequest, "invalid_request", "name is required"},
		{auth.ErrBadPassword, http.StatusUnauthorized, "unauthorized", ""},
		{auth.ErrTooManyAttempts, http.StatusTooManyRequests, "too_many_attempts", ""},
		{errNotImplemented, http.StatusNotImplemented, "not_implemented", ""},
		{errForbidden, http.StatusForbidden, "forbidden", ""},
		{errors.New("bifrost: dial tcp 127.0.0.1:18080: connection refused"), http.StatusBadGateway, "engine_error", ""},
	} {
		t.Run(tc.code, func(t *testing.T) {
			s := New(Deps{})
			rec := httptest.NewRecorder()
			s.writeError(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/x", http.NoBody), tc.err)
			if rec.Code != tc.status {
				t.Errorf("status = %d, want %d", rec.Code, tc.status)
			}
			body := rec.Body.Bytes()
			if got := errCode(t, body); got != tc.code {
				t.Errorf("code = %q, want %q", got, tc.code)
			}
			if !strings.Contains(string(body), tc.contains) {
				t.Errorf("body %s does not mention %q", body, tc.contains)
			}
			for _, leak := range []string{"bifrost", "127.0.0.1", "reconcile:", "store:"} {
				if strings.Contains(string(body), leak) {
					t.Errorf("body %s leaks %q", body, leak)
				}
			}
		})
	}
}

func TestPage(t *testing.T) {
	items := make([]int, 120)
	for i := range items {
		items[i] = i
	}
	ptr := func(n int) *int { return &n }

	got, total := page(items, nil, nil)
	if len(got) != 50 || total != 120 || got[0] != 0 {
		t.Errorf("default page = len %d total %d", len(got), total)
	}
	got, total = page(items, ptr(50), ptr(100))
	if len(got) != 20 || total != 120 || got[0] != 100 {
		t.Errorf("last page = len %d total %d", len(got), total)
	}
	if got, _ = page(items, ptr(1000), nil); len(got) != 120 {
		t.Errorf("limit 1000 = len %d, want all 120 (capped at 200)", len(got))
	}
	big := make([]int, 300)
	if got, total = page(big, ptr(1000), nil); len(got) != 200 || total != 300 {
		t.Errorf("limit 1000 of 300 = len %d total %d, want 200 300", len(got), total)
	}
	if got, _ = page(items, nil, ptr(500)); len(got) != 0 || got == nil {
		t.Errorf("offset past the end = %v, want empty non-nil", got)
	}

	q := "RouT"
	if !matches(nil, "x") || !matches(&q, "a", "openrouter") || matches(&q, "abc") {
		t.Error("matches")
	}
}
