package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/47vigen/remora/internal/alias"
)

// decode unmarshals body into v, failing the test on error.
func decode(t *testing.T, body []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(body, v); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
}

type providerJSON struct {
	Name     string  `json:"name"`
	Kind     string  `json:"kind"`
	Type     *string `json:"type"`
	BaseURL  *string `json:"base_url"`
	KeyCount *int    `json:"key_count"`
	Status   *string `json:"status"`
	Keys     []struct {
		Name  string  `json:"name"`
		Value *string `json:"value"`
	} `json:"keys"`
}

func providerStatus(t *testing.T, env *testEnv, name string) string {
	t.Helper()
	code, body := env.do(t, env.client, http.MethodGet, "/api/providers/"+name, "")
	if code != http.StatusOK {
		t.Fatalf("get %s = %d %s", name, code, body)
	}
	var p providerJSON
	decode(t, body, &p)
	if p.Status == nil {
		t.Fatalf("get %s has no status: %s", name, body)
	}
	return *p.Status
}

func TestProviderStatus(t *testing.T) {
	env := newTestEnv(t)
	for _, name := range []string{"or", "ag", "xs"} {
		if code, body := env.do(t, env.client, http.MethodPost, "/api/providers",
			`{"name":"`+name+`","kind":"openai-compatible","base_url":"http://upstream.test/v1","keys":[{"name":"main","value":"k"}]}`); code != http.StatusCreated {
			t.Fatalf("create %s = %d %s", name, code, body)
		}
	}
	if got := providerStatus(t, env, "or"); got != "unknown" {
		t.Errorf("never tested = %q, want unknown", got)
	}

	if code, body := env.do(t, env.client, http.MethodPost, "/api/providers/or/test", ""); code != http.StatusOK {
		t.Fatalf("test = %d %s", code, body)
	}
	if got := providerStatus(t, env, "or"); got != "ok" {
		t.Errorf("tested ok = %q, want ok", got)
	}

	// ag serves two models through aliases; one of them is cooling.
	env.aliases.m = map[string]alias.Alias{
		"fast": {Name: "fast", Kind: alias.KindChat, Members: []alias.Member{{Ref: "ag/a"}, {Ref: "ag/b"}, {Ref: "xs/c"}}},
	}
	until := time.Now().Add(time.Minute)
	env.aliases.setCooling(
		Cooling{Target: alias.Target{Provider: "ag", Model: "a"}, Until: until},
		Cooling{Target: alias.Target{Provider: "xs", Model: "c"}, Until: until},
	)
	if got := providerStatus(t, env, "ag"); got != "degraded" {
		t.Errorf("one of two models cooling = %q, want degraded", got)
	}
	if got := providerStatus(t, env, "xs"); got != "down" {
		t.Errorf("every model cooling = %q, want down", got)
	}

	env.providers.setTest("or", ProviderTest{OK: false, Message: "401 from upstream"})
	if code, body := env.do(t, env.client, http.MethodPost, "/api/providers/or/test", ""); code != http.StatusOK {
		t.Fatalf("test = %d %s", code, body)
	}
	if got := providerStatus(t, env, "or"); got != "down" {
		t.Errorf("test failed = %q, want down", got)
	}

	// The list reports the same status.
	code, body := env.do(t, env.client, http.MethodGet, "/api/providers", "")
	if code != http.StatusOK {
		t.Fatalf("list = %d %s", code, body)
	}
	var list struct{ Items []providerJSON }
	decode(t, body, &list)
	got := map[string]string{}
	for _, p := range list.Items {
		got[p.Name] = *p.Status
	}
	if got["or"] != "down" || got["ag"] != "degraded" || got["xs"] != "down" {
		t.Errorf("list statuses = %v", got)
	}
}

func TestProvidersCRUD(t *testing.T) {
	env := newTestEnv(t)
	code, body := env.do(t, env.client, http.MethodPost, "/api/providers",
		`{"name":"openrouter","kind":"builtin","type":"openrouter","keys":[{"name":"main","value":"k1"},{"name":"spare","value":"k2"}]}`)
	if code != http.StatusCreated {
		t.Fatalf("create = %d %s", code, body)
	}
	check := func(what string, body []byte) {
		t.Helper()
		var p providerJSON
		decode(t, body, &p)
		if p.Name != "openrouter" || p.Kind != "builtin" || p.Type == nil || *p.Type != "openrouter" {
			t.Errorf("%s = %s", what, body)
		}
		if p.KeyCount == nil || *p.KeyCount != 2 || p.Status == nil || *p.Status != "unknown" {
			t.Errorf("%s key_count/status = %s", what, body)
		}
		if len(p.Keys) != 2 || p.Keys[0].Name != "main" || p.Keys[1].Name != "spare" {
			t.Errorf("%s keys = %s", what, body)
		}
		for _, k := range p.Keys {
			if k.Value != nil {
				t.Errorf("%s shows a key value: %s", what, body)
			}
		}
		if strings.Contains(string(body), "k1") || strings.Contains(string(body), "k2") {
			t.Errorf("%s leaks a secret: %s", what, body)
		}
	}
	check("create", body)

	code, body = env.do(t, env.client, http.MethodGet, "/api/providers/openrouter", "")
	if code != http.StatusOK {
		t.Fatalf("get = %d %s", code, body)
	}
	check("get", body)

	code, body = env.do(t, env.client, http.MethodPut, "/api/providers/openrouter",
		`{"name":"openrouter","kind":"builtin","type":"openrouter"}`)
	if code != http.StatusOK {
		t.Fatalf("put = %d %s", code, body)
	}
	check("put", body)
	stored, _ := env.providers.Get(t.Context(), "openrouter")
	if len(stored.Keys) != 2 || stored.Keys[0].Value != "k1" || stored.Keys[1].Value != "k2" {
		t.Errorf("stored keys after PUT without keys = %+v", stored.Keys)
	}

	if code, body := env.do(t, env.client, http.MethodPut, "/api/providers/openrouter",
		`{"name":"other","kind":"builtin","type":"openrouter"}`); code != http.StatusBadRequest {
		t.Errorf("put with a different name = %d %s", code, body)
	}

	if code, body := env.do(t, env.client, http.MethodDelete, "/api/providers/openrouter", ""); code != http.StatusNoContent {
		t.Fatalf("delete = %d %s", code, body)
	}
	code, body = env.do(t, env.client, http.MethodGet, "/api/providers/openrouter", "")
	if code != http.StatusNotFound || errCode(t, body) != "not_found" {
		t.Errorf("get after delete = %d %s", code, body)
	}
}

func TestListProvidersFilters(t *testing.T) {
	env := newTestEnv(t)
	for _, body := range []string{
		`{"name":"openrouter","kind":"builtin","type":"openrouter","keys":[{"name":"main","value":"k"}]}`,
		`{"name":"local","kind":"openai-compatible","base_url":"http://upstream.test/v1","keys":[{"name":"main","value":"k"}]}`,
		`{"name":"speech","kind":"soniox","keys":[{"name":"main","value":"k"}]}`,
	} {
		if code, resp := env.do(t, env.client, http.MethodPost, "/api/providers", body); code != http.StatusCreated {
			t.Fatalf("create = %d %s", code, resp)
		}
	}
	for _, tc := range []struct {
		query string
		want  []string
	}{
		{"", []string{"local", "openrouter", "speech"}},
		{"?kind=openai-compatible", []string{"local"}},
		{"?type=openrouter", []string{"openrouter"}},
		{"?q=ROUT", []string{"openrouter"}}, // case-insensitive
		{"?limit=1&offset=1", []string{"openrouter"}},
	} {
		code, body := env.do(t, env.client, http.MethodGet, "/api/providers"+tc.query, "")
		if code != http.StatusOK {
			t.Fatalf("list %s = %d %s", tc.query, code, body)
		}
		var list struct {
			Items []providerJSON
			Total int
		}
		decode(t, body, &list)
		var names []string
		for _, p := range list.Items {
			names = append(names, p.Name)
		}
		if strings.Join(names, ",") != strings.Join(tc.want, ",") {
			t.Errorf("list %s = %v, want %v", tc.query, names, tc.want)
		}
		wantTotal := len(tc.want)
		if strings.HasPrefix(tc.query, "?limit") {
			wantTotal = 3
		}
		if list.Total != wantTotal {
			t.Errorf("list %s total = %d, want %d", tc.query, list.Total, wantTotal)
		}
	}
}

func TestCreateProviderValidation(t *testing.T) {
	env := newTestEnv(t)
	if code, body := env.do(t, env.client, http.MethodPost, "/api/providers",
		`{"name":"taken","kind":"soniox","keys":[{"name":"main","value":"k"}]}`); code != http.StatusCreated {
		t.Fatalf("create = %d %s", code, body)
	}
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"empty name", `{"name":"","kind":"soniox"}`, http.StatusBadRequest},
		{"slash in name", `{"name":"a/b","kind":"soniox"}`, http.StatusBadRequest},
		{"upper case name", `{"name":"OpenRouter","kind":"builtin","type":"openrouter"}`, http.StatusBadRequest},
		{"non-canonical kind", `{"name":"x","kind":"openai"}`, http.StatusBadRequest},
		{"builtin without type", `{"name":"x","kind":"builtin"}`, http.StatusBadRequest},
		{"openai-compatible with a type", `{"name":"x","kind":"openai-compatible","type":"openrouter","base_url":"http://upstream.test"}`, http.StatusBadRequest},
		{"openai-compatible without base_url", `{"name":"x","kind":"openai-compatible"}`, http.StatusBadRequest},
		{"typesafe-compatible without base_url", `{"name":"x","kind":"typesafe-compatible"}`, http.StatusBadRequest},
		{"key without a value", `{"name":"x","kind":"soniox","keys":[{"name":"main"}]}`, http.StatusBadRequest},
		{"key without a name", `{"name":"x","kind":"soniox","keys":[{"name":"","value":"k"}]}`, http.StatusBadRequest},
		{"duplicate key names", `{"name":"x","kind":"soniox","keys":[{"name":"a","value":"k"},{"name":"a","value":"j"}]}`, http.StatusBadRequest},
		{"duplicate", `{"name":"taken","kind":"soniox","keys":[{"name":"main","value":"k"}]}`, http.StatusConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, body := env.do(t, env.client, http.MethodPost, "/api/providers", tc.body)
			if code != tc.status {
				t.Errorf("status = %d %s, want %d", code, body, tc.status)
			}
			want := "invalid_request"
			if tc.status == http.StatusConflict {
				want = "conflict"
			}
			if got := errCode(t, body); got != want {
				t.Errorf("code = %q, want %q", got, want)
			}
		})
	}
}

func TestTestProvider(t *testing.T) {
	env := newTestEnv(t)
	if code, body := env.do(t, env.client, http.MethodPost, "/api/providers",
		`{"name":"or","kind":"builtin","type":"openrouter","keys":[{"name":"main","value":"k"}]}`); code != http.StatusCreated {
		t.Fatalf("create = %d %s", code, body)
	}
	env.providers.setTest("or", ProviderTest{OK: false, Latency: 120 * time.Millisecond, Message: "401 from upstream"})
	code, body := env.do(t, env.client, http.MethodPost, "/api/providers/or/test", "")
	if code != http.StatusOK || strings.TrimSpace(string(body)) != `{"latency_ms":120,"message":"401 from upstream","ok":false}` {
		t.Errorf("test = %d %s", code, body)
	}
	if code, body := env.do(t, env.client, http.MethodPost, "/api/providers/none/test", ""); code != http.StatusNotFound {
		t.Errorf("test unknown = %d %s", code, body)
	}
}
