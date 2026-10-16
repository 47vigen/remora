package api

import (
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/47vigen/remora/internal/alias"

	"github.com/47vigen/remora/internal/accounts"
)

type accountJSON struct {
	ID            string     `json:"id"`
	Provider      string     `json:"provider"`
	Email         string     `json:"email"`
	Enabled       bool       `json:"enabled"`
	Priority      *int       `json:"priority"`
	Status        string     `json:"status"`
	Message       string     `json:"message"`
	CooldownUntil *time.Time `json:"cooldown_until"`
	Quota         []struct {
		Model             string     `json:"model"`
		RemainingFraction *float64   `json:"remaining_fraction"`
		ResetAt           *time.Time `json:"reset_at"`
	} `json:"quota"`
	Models []string `json:"models"`
}

type accountListJSON struct {
	Items []accountJSON `json:"items"`
	Total int           `json:"total"`
}

func TestAccountLoginFlow(t *testing.T) {
	env := newTestEnv(t)
	code, body := env.do(t, env.client, http.MethodPost, "/api/accounts/logins", `{"provider":"codex"}`)
	if code != http.StatusCreated {
		t.Fatalf("start = %d %s", code, body)
	}
	var l struct{ State, URL, UserCode string }
	decode(t, body, &l)
	if l.State != "s1" || !strings.HasPrefix(l.URL, "https://") || strings.Contains(string(body), "user_code") {
		t.Errorf("start = %s", body)
	}

	code, body = env.do(t, env.client, http.MethodGet, "/api/accounts/logins/s1", "")
	if code != http.StatusOK || strings.TrimSpace(string(body)) != `{"status":"wait"}` {
		t.Errorf("status = %d %s, want wait", code, body)
	}

	const redirect = "http://localhost:1455/auth/callback?code=c&state=s1"
	code, body = env.do(t, env.client, http.MethodPost, "/api/accounts/logins/s1/complete", `{"redirect_url":"`+redirect+`"}`)
	if code != http.StatusAccepted {
		t.Fatalf("complete = %d %s", code, body)
	}
	if got := env.accounts.completed; len(got) != 1 || got[0] != [2]string{"s1", redirect} {
		t.Errorf("fake saw %v, want (s1, %s)", got, redirect)
	}

	code, body = env.do(t, env.client, http.MethodGet, "/api/accounts/logins/s1", "")
	if code != http.StatusOK || strings.TrimSpace(string(body)) != `{"status":"ok"}` {
		t.Errorf("status = %d %s, want ok", code, body)
	}

	code, body = env.do(t, env.client, http.MethodGet, "/api/accounts/logins/nope", "")
	if code != http.StatusNotFound || errCode(t, body) != "not_found" {
		t.Errorf("unknown status = %d %s", code, body)
	}
	code, body = env.do(t, env.client, http.MethodPost, "/api/accounts/logins", `{"provider":"nope"}`)
	if code != http.StatusBadRequest || errCode(t, body) != "invalid_request" {
		t.Errorf("unknown provider = %d %s", code, body)
	}
	code, body = env.do(t, env.client, http.MethodPost, "/api/accounts/logins", `{"provider":""}`)
	if code != http.StatusBadRequest {
		t.Errorf("empty provider = %d %s", code, body)
	}
}

func TestDeviceCodeLogin(t *testing.T) {
	env := newTestEnv(t)
	env.accounts.next = Login{State: "s2", URL: "https://login.example/device", UserCode: "ABCD-1234"}
	code, body := env.do(t, env.client, http.MethodPost, "/api/accounts/logins", `{"provider":"grok"}`)
	if code != http.StatusCreated {
		t.Fatalf("start = %d %s", code, body)
	}
	var l struct {
		State    string `json:"state"`
		URL      string `json:"url"`
		UserCode string `json:"user_code"`
	}
	decode(t, body, &l)
	if l.State != "s2" || l.URL != "https://login.example/device" || l.UserCode != "ABCD-1234" {
		t.Errorf("start = %s", body)
	}
}

func TestCancelAccountLogin(t *testing.T) {
	env := newTestEnv(t)
	if code, body := env.do(t, env.client, http.MethodPost, "/api/accounts/logins", `{"provider":"codex"}`); code != http.StatusCreated {
		t.Fatalf("start = %d %s", code, body)
	}
	if code, body := env.do(t, env.client, http.MethodDelete, "/api/accounts/logins/s1", ""); code != http.StatusNoContent {
		t.Errorf("cancel = %d %s", code, body)
	}
	if !slices.Equal(env.accounts.cancelled, []string{"s1"}) {
		t.Errorf("fake cancelled %v, want [s1]", env.accounts.cancelled)
	}
	code, body := env.do(t, env.client, http.MethodDelete, "/api/accounts/logins/s9", "")
	if code != http.StatusNotFound || errCode(t, body) != "not_found" {
		t.Errorf("cancel unknown = %d %s", code, body)
	}
}

func TestCompleteLoginRejectsBadURL(t *testing.T) {
	env := newTestEnv(t)
	if code, body := env.do(t, env.client, http.MethodPost, "/api/accounts/logins", `{"provider":"codex"}`); code != http.StatusCreated {
		t.Fatalf("start = %d %s", code, body)
	}
	for _, u := range []string{"", "not a url", "http://localhost/cb", "/cb?code=c", "http://localhost/cb?state=s1"} {
		code, body := env.do(t, env.client, http.MethodPost, "/api/accounts/logins/s1/complete", `{"redirect_url":"`+u+`"}`)
		if code != http.StatusBadRequest || errCode(t, body) != "invalid_request" {
			t.Errorf("complete %q = %d %s, want 400", u, code, body)
		}
	}
	if len(env.accounts.completed) != 0 {
		t.Errorf("fake called with %v", env.accounts.completed)
	}
}

func TestCompleteLoginWithoutState(t *testing.T) {
	env := newTestEnv(t)
	if code, body := env.do(t, env.client, http.MethodPost, "/api/accounts/logins", `{"provider":"codex"}`); code != http.StatusCreated {
		t.Fatalf("start = %d %s", code, body)
	}
	for _, u := range []string{"http://localhost:1455/cb?code=c", "http://localhost:1455/cb?error=access_denied"} {
		code, body := env.do(t, env.client, http.MethodPost, "/api/accounts/logins/s1/complete", `{"redirect_url":"`+u+`"}`)
		if code != http.StatusAccepted {
			t.Errorf("complete %q = %d %s, want 202", u, code, body)
		}
	}
	if len(env.accounts.completed) != 2 || env.accounts.completed[0][1] != "http://localhost:1455/cb?code=c" {
		t.Errorf("fake saw %v", env.accounts.completed)
	}
}

func TestListAccounts(t *testing.T) {
	env := newTestEnv(t)
	until := time.Date(2026, 10, 14, 13, 0, 0, 0, time.UTC)
	frac := 0.25
	env.accounts.accounts = []Account{
		{
			ID: "1", Provider: "antigravity", Email: "first@example.com", Enabled: true, Status: "cooling",
			Message: "rate limited", CooldownUntil: &until,
			Quota:  []Quota{{Model: "antigravity/m1", RemainingFraction: &frac, ResetAt: &until}},
			Models: []string{"antigravity/m1", "antigravity/m2"},
		},
		{ID: "2", Provider: "codex", Email: "second@example.com", Enabled: true, Status: "active"},
		{ID: "3", Provider: "antigravity", Email: "third@example.com", Status: "disabled"},
	}

	code, body := env.do(t, env.client, http.MethodGet, "/api/accounts?provider=antigravity", "")
	if code != http.StatusOK {
		t.Fatalf("list = %d %s", code, body)
	}
	var list accountListJSON
	decode(t, body, &list)
	if list.Total != 2 || len(list.Items) != 2 || list.Items[0].ID != "1" || list.Items[1].ID != "3" {
		t.Fatalf("list = %s", body)
	}
	a := list.Items[0]
	if a.Provider != "antigravity" || a.Email != "first@example.com" || !a.Enabled || a.Status != "cooling" ||
		a.Message != "rate limited" || a.CooldownUntil == nil || !a.CooldownUntil.Equal(until) ||
		!slices.Equal(a.Models, []string{"antigravity/m1", "antigravity/m2"}) || len(a.Quota) != 1 ||
		a.Quota[0].Model != "antigravity/m1" || a.Quota[0].RemainingFraction == nil || *a.Quota[0].RemainingFraction != 0.25 ||
		a.Quota[0].ResetAt == nil || !a.Quota[0].ResetAt.Equal(until) {
		t.Errorf("first = %+v", a)
	}
	// An account without quota or models still answers both as arrays.
	if !strings.Contains(string(body), `"id":"3"`) || strings.Count(string(body), `"quota":[]`) != 1 ||
		strings.Count(string(body), `"models":[]`) != 1 {
		t.Errorf("list = %s, want [] for the third account's quota and models", body)
	}

	code, body = env.do(t, env.client, http.MethodGet, "/api/accounts?q=SECOND", "")
	decode(t, body, &list)
	if code != http.StatusOK || list.Total != 1 || list.Items[0].ID != "2" {
		t.Errorf("q=SECOND = %d %s", code, body)
	}
	code, body = env.do(t, env.client, http.MethodGet, "/api/accounts?limit=1&offset=1", "")
	decode(t, body, &list)
	if code != http.StatusOK || list.Total != 3 || len(list.Items) != 1 || list.Items[0].ID != "2" {
		t.Errorf("page = %d %s", code, body)
	}
}

func TestAccountToggleDeleteReset(t *testing.T) {
	env := newTestEnv(t)
	until := time.Date(2026, 10, 14, 13, 0, 0, 0, time.UTC)
	env.accounts.accounts = []Account{{ID: "7", Provider: "codex", Email: "a@example.com", Enabled: true, Status: "active", CooldownUntil: &until}}

	code, body := env.do(t, env.client, http.MethodPatch, "/api/accounts/7", `{"enabled":false}`)
	if code != http.StatusOK {
		t.Fatalf("patch = %d %s", code, body)
	}
	var a accountJSON
	decode(t, body, &a)
	if a.ID != "7" || a.Enabled {
		t.Errorf("patch = %s", body)
	}
	if got, ok := env.accounts.enabled["7"]; !ok || got {
		t.Errorf("fake SetEnabled = %v %v, want (7, false)", got, ok)
	}

	if code, body := env.do(t, env.client, http.MethodPost, "/api/accounts/7/reset-cooldown", ""); code != http.StatusNoContent {
		t.Errorf("reset = %d %s", code, body)
	}
	if !slices.Equal(env.accounts.reset, []string{"7"}) {
		t.Errorf("fake reset %v", env.accounts.reset)
	}
	if code, body := env.do(t, env.client, http.MethodDelete, "/api/accounts/7", ""); code != http.StatusNoContent {
		t.Errorf("delete = %d %s", code, body)
	}
	if len(env.accounts.accounts) != 0 {
		t.Errorf("after delete = %+v", env.accounts.accounts)
	}

	for _, r := range []struct{ method, path, body string }{
		{http.MethodPatch, "/api/accounts/7", `{"enabled":true}`},
		{http.MethodPost, "/api/accounts/7/reset-cooldown", ""},
		{http.MethodDelete, "/api/accounts/7", ""},
	} {
		code, body := env.do(t, env.client, r.method, r.path, r.body)
		if code != http.StatusNotFound || errCode(t, body) != "not_found" {
			t.Errorf("%s %s unknown = %d %s", r.method, r.path, code, body)
		}
	}
}

func TestImportAccount(t *testing.T) {
	env := newTestEnv(t)
	const cred = `{"email":"imp@example.com","refresh_token":"rt-test","type":"antigravity"}` //nolint:gosec // G101: a test value.
	code, body := env.do(t, env.client, http.MethodPost, "/api/accounts/import", `{"provider":"antigravity","credential":`+cred+`}`)
	if code != http.StatusCreated {
		t.Fatalf("import = %d %s", code, body)
	}
	var a accountJSON
	decode(t, body, &a)
	if a.ID == "" || a.Provider != "antigravity" || a.Email != "imp@example.com" || !a.Enabled {
		t.Errorf("import = %s", body)
	}
	if strings.Contains(string(body), "rt-test") {
		t.Errorf("import answer %s carries the credential", body)
	}
	if got := env.accounts.imported; len(got) != 1 || got[0].provider != "antigravity" || string(got[0].credential) != cred {
		t.Errorf("fake got %+v, want %s", got, cred)
	}

	for _, b := range []string{
		`{"provider":"antigravity","credential":{"type":"codex"}}`,
		`{"provider":"antigravity","credential":{}}`,
		`{"provider":"","credential":{"type":"antigravity"}}`,
		`{"provider":"antigravity","credential":{"type":7}}`,
		`{"provider":"antigravity","credential":"x"}`,
	} {
		code, body := env.do(t, env.client, http.MethodPost, "/api/accounts/import", b)
		if code != http.StatusBadRequest || errCode(t, body) != "invalid_request" {
			t.Errorf("import %s = %d %s, want 400", b, code, body)
		}
	}
}

func TestGetAccountPool(t *testing.T) {
	env := newTestEnv(t)
	env.accounts.pool = []accounts.PoolStatus{{Provider: "antigravity", Enabled: 20, Out: []accounts.OutAccount{
		{ID: 3, Label: "three@example.com", Reason: "signed out"}, {ID: 7, Label: "seven@example.com", Reason: "missing"},
	}}}
	code, body := env.do(t, env.client, http.MethodGet, "/api/accounts/pool", "")
	want := `[{"enabled":20,"out":[{"id":"3","label":"three@example.com","reason":"signed out"},` +
		`{"id":"7","label":"seven@example.com","reason":"missing"}],"provider":"antigravity"}]`
	if code != http.StatusOK || strings.TrimSpace(string(body)) != want {
		t.Fatalf("pool = %d %s", code, body)
	}
	if code, _ := env.do(t, http.DefaultClient, http.MethodGet, "/api/accounts/pool", ""); code != http.StatusUnauthorized {
		t.Errorf("anonymous pool = %d, want 401", code)
	}
	env.accounts.pool = nil
	if _, body := env.do(t, env.client, http.MethodGet, "/api/accounts/pool", ""); strings.TrimSpace(string(body)) != "[]" {
		t.Errorf("empty pool = %s, want []", body)
	}
}

func TestListAccountStrategies(t *testing.T) {
	env := newTestEnv(t)
	env.accounts.strats = map[string]alias.Strategy{"codex": alias.StrategyP2C}
	code, body := env.do(t, env.client, http.MethodGet, "/api/accounts/strategies", "")
	if code != http.StatusOK {
		t.Fatalf("list = %d %s", code, body)
	}
	want := `{"items":[{"provider":"antigravity","strategy":"round-robin"},{"provider":"codex","strategy":"p2c"},{"provider":"grok","strategy":"round-robin"}],"total":3}`
	if strings.TrimSpace(string(body)) != want {
		t.Errorf("list = %s\nwant %s", body, want)
	}
}

func TestSetAccountStrategy(t *testing.T) {
	env := newTestEnv(t)
	code, body := env.do(t, env.client, http.MethodPut, "/api/accounts/strategies/codex", `{"strategy":"p2c"}`)
	if code != http.StatusOK || strings.TrimSpace(string(body)) != `{"provider":"codex","strategy":"p2c"}` {
		t.Fatalf("put = %d %s", code, body)
	}
	if env.accounts.strats["codex"] != alias.StrategyP2C {
		t.Errorf("stored %v", env.accounts.strats)
	}
	code, body = env.do(t, env.client, http.MethodPut, "/api/accounts/strategies/codex", `{"strategy":"bogus"}`)
	if code != http.StatusBadRequest || !strings.Contains(string(body), "strategy must be one of priority, fill-first, round-robin, random, p2c, least-used") {
		t.Errorf("bogus strategy = %d %s", code, body)
	}
	if code, body := env.do(t, env.client, http.MethodPut, "/api/accounts/strategies/nope", `{"strategy":"p2c"}`); code != http.StatusBadRequest {
		t.Errorf("unknown provider = %d %s", code, body)
	}
}

func TestUpdateAccountPartial(t *testing.T) {
	env := newTestEnv(t)
	env.accounts.accounts = []Account{{ID: "7", Provider: "codex", Enabled: true, Status: "active"}}
	code, body := env.do(t, env.client, http.MethodPatch, "/api/accounts/7", `{"priority":4}`)
	if code != http.StatusOK {
		t.Fatalf("priority = %d %s", code, body)
	}
	var a accountJSON
	decode(t, body, &a)
	if a.Priority == nil || *a.Priority != 4 || !a.Enabled {
		t.Errorf("answer = %s", body)
	}
	if _, toggled := env.accounts.enabled["7"]; toggled || env.accounts.priority["7"] != 4 {
		t.Errorf("priority edit: enabled calls %v, priority %v", env.accounts.enabled, env.accounts.priority)
	}
	if code, body := env.do(t, env.client, http.MethodPatch, "/api/accounts/7", `{"enabled":false}`); code != http.StatusOK {
		t.Fatalf("enabled = %d %s", code, body)
	}
	if len(env.accounts.priority) != 1 || env.accounts.enabled["7"] {
		t.Errorf("enabled edit: priority calls %v, enabled %v", env.accounts.priority, env.accounts.enabled)
	}
	if code, body := env.do(t, env.client, http.MethodPatch, "/api/accounts/7", `{}`); code != http.StatusBadRequest {
		t.Errorf("empty body = %d %s", code, body)
	}
}
