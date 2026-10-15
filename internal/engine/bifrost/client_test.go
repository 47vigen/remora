package bifrost

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"
)

// recorded is one request the fake engine saw.
type recorded struct {
	method, path, contentType string
	user, password            string
	basicOK                   bool
	body                      map[string]any
}

// fakeEngine records every request and answers each one with the canned status and body for its "METHOD path".
type fakeEngine struct {
	mu      sync.Mutex
	reqs    []recorded
	answers map[string]answer
}

type answer struct {
	status int
	body   string
}

func newFakeEngine(t *testing.T, answers map[string]answer) (*fakeEngine, *Client) {
	t.Helper()
	f := &fakeEngine{answers: answers}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	return f, New(srv.URL, "admin", "secret", srv.Client())
}

func (f *fakeEngine) serve(w http.ResponseWriter, r *http.Request) {
	rec := recorded{method: r.Method, path: r.URL.EscapedPath(), contentType: r.Header.Get("Content-Type")}
	rec.user, rec.password, rec.basicOK = r.BasicAuth()
	if raw, _ := io.ReadAll(r.Body); len(raw) > 0 {
		_ = json.Unmarshal(raw, &rec.body)
	}
	f.mu.Lock()
	f.reqs = append(f.reqs, rec)
	a, ok := f.answers[r.Method+" "+rec.path]
	f.mu.Unlock()
	if !ok {
		a = answer{http.StatusOK, `{"message":"ok"}`}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(a.status)
	_, _ = io.WriteString(w, a.body)
}

func (f *fakeEngine) last(t *testing.T) recorded {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.reqs) == 0 {
		t.Fatal("no request reached the fake engine")
	}
	return f.reqs[len(f.reqs)-1]
}

// subset reports whether every key in want is in got with an equal value; nested maps are compared as subsets too.
func subset(t *testing.T, prefix string, got, want map[string]any) {
	t.Helper()
	for k, wv := range want {
		gv, ok := got[k]
		if !ok {
			t.Errorf("%s%s: missing (body %v)", prefix, k, got)
			continue
		}
		if wm, ok := wv.(map[string]any); ok {
			gm, ok := gv.(map[string]any)
			if !ok {
				t.Errorf("%s%s: got %T %v, want object", prefix, k, gv, gv)
				continue
			}
			subset(t, prefix+k+".", gm, wm)
			continue
		}
		if !reflect.DeepEqual(gv, wv) {
			t.Errorf("%s%s: got %#v, want %#v", prefix, k, gv, wv)
		}
	}
}

func TestClient_Requests(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name     string
		call     func(c *Client) error
		method   string
		path     string
		wantBody map[string]any // subset match on the decoded JSON
		absent   []string       // top-level keys that must not be sent
	}{
		{"custom openai provider", func(c *Client) error {
			return c.CreateProvider(ctx, Provider{Name: "up-a", BaseType: "openai", BaseURL: "http://fakes:9090/u/a", AllowPrivateNetwork: true})
		}, "POST", "/api/providers", map[string]any{
			"provider":               "up-a",
			"custom_provider_config": map[string]any{"base_provider_type": "openai", "is_key_less": false},
			"network_config": map[string]any{
				"base_url": "http://fakes:9090/u/a", "max_retries": 0.0,
				"allow_private_network": true, "default_request_timeout_in_seconds": 600.0,
				"stream_idle_timeout_in_seconds": 300.0,
			},
			"concurrency_and_buffer_size": map[string]any{"concurrency": 1000.0, "buffer_size": 5000.0},
		}, nil},
		{"custom typesafe provider", func(c *Client) error {
			return c.CreateProvider(ctx, Provider{Name: "ts-a", BaseType: "typesafe", BaseURL: "http://fakes:9090/t"})
		}, "POST", "/api/providers", map[string]any{
			"provider":               "ts-a",
			"custom_provider_config": map[string]any{"base_provider_type": "typesafe"},
		}, nil},
		{"standard provider has no custom config", func(c *Client) error {
			return c.CreateProvider(ctx, Provider{Name: "openrouter"})
		}, "POST", "/api/providers", map[string]any{
			"provider":       "openrouter",
			"network_config": map[string]any{"max_retries": 0.0},
		}, []string{"custom_provider_config"}},
		{"update provider", func(c *Client) error {
			return c.UpdateProvider(ctx, Provider{Name: "up-a", BaseType: "openai", BaseURL: "http://fakes:9090/u/a"})
		}, "PUT", "/api/providers/up-a", map[string]any{
			"network_config":              map[string]any{"max_retries": 0.0, "base_url": "http://fakes:9090/u/a"},
			"concurrency_and_buffer_size": map[string]any{"concurrency": 1000.0, "buffer_size": 5000.0},
			"custom_provider_config":      map[string]any{"base_provider_type": "openai"},
		}, []string{"provider"}},
		{"allowed requests and timeout", func(c *Client) error {
			return c.CreateProvider(ctx, Provider{
				Name: "soniox", BaseType: "openai", BaseURL: "http://fakes:9090/s",
				AllowedRequests: []string{"transcription"}, RequestTimeout: 1860 * time.Second,
			})
		}, "POST", "/api/providers", map[string]any{
			"custom_provider_config": map[string]any{"allowed_requests": map[string]any{"transcription": true}},
			"network_config":         map[string]any{"default_request_timeout_in_seconds": 1860.0},
		}, nil},
		{"provider name is escaped", func(c *Client) error {
			return c.DeleteProvider(ctx, "a/b")
		}, "DELETE", "/api/providers/a%2Fb", nil, nil},
		{"create key", func(c *Client) error {
			return c.CreateKey(ctx, "up-a", Key{ID: "k1", Name: "main", Value: "test-upstream-key", Enabled: true})
		}, "POST", "/api/providers/up-a/keys", map[string]any{
			"id": "k1", "name": "main", "value": "test-upstream-key",
			"models": []any{"*"}, "weight": 1.0, "enabled": true,
		}, nil},
		{"update key", func(c *Client) error {
			return c.UpdateKey(ctx, "up-a", Key{ID: "k1", Name: "main", Value: "test-upstream-key-2"})
		}, "PUT", "/api/providers/up-a/keys/k1", map[string]any{
			"id": "k1", "value": "test-upstream-key-2", "models": []any{"*"}, "enabled": false,
		}, nil},
		{"delete key", func(c *Client) error {
			return c.DeleteKey(ctx, "up-a", "k1")
		}, "DELETE", "/api/providers/up-a/keys/k1", nil, nil},
		{"create virtual key", func(c *Client) error {
			_, err := c.CreateVirtualKey(ctx, VirtualKey{Name: "remora:key:c1", Active: true})
			return err
		}, "POST", "/api/governance/virtual-keys", map[string]any{
			"name": "remora:key:c1", "allow_all_providers": true, "is_active": true,
			"description": "managed by remora",
		}, nil},
		{"update virtual key", func(c *Client) error {
			return c.UpdateVirtualKey(ctx, VirtualKey{ID: "vk1", Name: "remora:key:c1", Active: false})
		}, "PUT", "/api/governance/virtual-keys/vk1", map[string]any{
			"name": "remora:key:c1", "allow_all_providers": true, "is_active": false,
		}, nil},
		{"delete virtual key", func(c *Client) error {
			return c.DeleteVirtualKey(ctx, "vk1")
		}, "DELETE", "/api/governance/virtual-keys/vk1", nil, nil},
		{"create rule", func(c *Client) error {
			_, err := c.CreateRoutingRule(ctx, RoutingRule{
				Name: "remora:alias:fast", CEL: `model == "fast"`,
				Target: Route{"up-a", "m1"}, Fallbacks: []Route{{"up-b", "m1"}},
			})
			return err
		}, "POST", "/api/routing/rules", map[string]any{
			"name": "remora:alias:fast", "cel_expression": `model == "fast"`,
			"enabled": true, "scope": "global", "priority": 0.0,
			"targets":   []any{map[string]any{"provider": "up-a", "model": "m1", "weight": 1.0}},
			"fallbacks": []any{map[string]any{"provider": "up-b", "model": "m1"}},
		}, nil},
		{"create spread rule", func(c *Client) error {
			_, err := c.CreateRoutingRule(ctx, RoutingRule{
				Name: "remora:alias:fast", CEL: `model == "fast"`,
				Target: Route{"up-a", "m1"}, Spread: []Route{{"up-b", "m1"}, {"up-c", "m1"}, {"up-d", "m1"}},
			})
			return err
		}, "POST", "/api/routing/rules", map[string]any{
			"targets": []any{
				map[string]any{"provider": "up-a", "model": "m1", "weight": 0.25},
				map[string]any{"provider": "up-b", "model": "m1", "weight": 0.25},
				map[string]any{"provider": "up-c", "model": "m1", "weight": 0.25},
				map[string]any{"provider": "up-d", "model": "m1", "weight": 0.25},
			},
		}, nil},
		{"update rule", func(c *Client) error {
			return c.UpdateRoutingRule(ctx, RoutingRule{
				ID: "r1", Name: "remora:alias:fast", CEL: `model == "fast"`, Target: Route{"up-a", "m1"}, Priority: 3,
			})
		}, "PUT", "/api/routing/rules/r1", map[string]any{
			"name": "remora:alias:fast", "priority": 3.0,
			"fallbacks": []any{}, // always sent, so an update can clear the chain
		}, nil},
		{"rule target with incoming model", func(c *Client) error {
			_, err := c.CreateRoutingRule(ctx, RoutingRule{Name: "remora-oauth-x", CEL: "true", Target: Route{"remora-oauth", ""}})
			return err
		}, "POST", "/api/routing/rules", map[string]any{
			"targets": []any{map[string]any{"provider": "remora-oauth", "model": nil, "weight": 1.0}},
		}, nil},
		{"delete rule", func(c *Client) error {
			return c.DeleteRoutingRule(ctx, "r1")
		}, "DELETE", "/api/routing/rules/r1", nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, c := newFakeEngine(t, map[string]answer{
				"POST /api/governance/virtual-keys": {200, `{"virtual_key":{"id":"vk1","value":"sk-bf-x"}}`},
				"POST /api/routing/rules":           {200, `{"rule":{"id":"r1"}}`},
			})
			if err := tc.call(c); err != nil {
				t.Fatalf("call: %v", err)
			}
			got := f.last(t)
			if got.method != tc.method || got.path != tc.path {
				t.Fatalf("request %s %s, want %s %s", got.method, got.path, tc.method, tc.path)
			}
			if !got.basicOK || got.user != "admin" || got.password != "secret" {
				t.Errorf("basic auth %q:%q (ok=%v), want admin:secret", got.user, got.password, got.basicOK)
			}
			if tc.wantBody != nil {
				if got.contentType != "application/json" {
					t.Errorf("Content-Type %q, want application/json", got.contentType)
				}
				subset(t, "", got.body, tc.wantBody)
			}
			for _, k := range tc.absent {
				if _, ok := got.body[k]; ok {
					t.Errorf("%s sent, want absent: %v", k, got.body[k])
				}
			}
		})
	}
}

func TestClient_KeyValueRejectsEngineReferences(t *testing.T) {
	_, c := newFakeEngine(t, nil)
	for _, v := range []string{"env.HOME", "vault.secret/x"} {
		if err := c.CreateKey(context.Background(), "up-a", Key{ID: "k1", Value: v}); err == nil {
			t.Errorf("CreateKey(%q): want error", v)
		}
		if err := c.UpdateKey(context.Background(), "up-a", Key{ID: "k1", Value: v}); err == nil {
			t.Errorf("UpdateKey(%q): want error", v)
		}
	}
}

func TestClient_CreateVirtualKeyReturnsIDAndValue(t *testing.T) {
	_, c := newFakeEngine(t, map[string]answer{
		"POST /api/governance/virtual-keys": {200, `{"message":"created","virtual_key":{"id":"vk1","name":"remora:key:c1","is_active":true,"value":{"value":"sk-bf-x"}}}`},
	})
	got, err := c.CreateVirtualKey(context.Background(), VirtualKey{Name: "remora:key:c1", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	want := VirtualKey{ID: "vk1", Name: "remora:key:c1", Value: "sk-bf-x", Active: true}
	if got != want {
		t.Fatalf("object value: got %+v, want %+v", got, want)
	}

	// The engine at transports/v2.3.0 encodes a virtual key's value as a plain string.
	_, c = newFakeEngine(t, map[string]answer{
		"POST /api/governance/virtual-keys": {200, `{"message":"created","virtual_key":{"id":"vk1","name":"remora:key:c1","is_active":true,"value":"sk-bf-x"}}`},
	})
	if got, err = c.CreateVirtualKey(context.Background(), VirtualKey{Name: "remora:key:c1", Active: true}); err != nil || got != want {
		t.Fatalf("string value: got %+v, %v; want %+v", got, err, want)
	}

	_, c = newFakeEngine(t, map[string]answer{
		"POST /api/governance/virtual-keys": {200, `{"message":"created","virtual_key":{"id":"vk1"}}`},
	})
	if _, err = c.CreateVirtualKey(context.Background(), VirtualKey{Name: "remora:key:c1"}); err == nil {
		t.Fatal("answer without value: want error")
	}
}

func TestClient_EmptyIDsNeverReachTheEngine(t *testing.T) {
	f, c := newFakeEngine(t, nil)
	ctx := context.Background()
	calls := map[string]error{
		"UpdateProvider":    c.UpdateProvider(ctx, Provider{}),
		"DeleteProvider":    c.DeleteProvider(ctx, ""),
		"ListKeys":          func() error { _, err := c.ListKeys(ctx, ""); return err }(),
		"CreateKey":         c.CreateKey(ctx, "", Key{ID: "k1", Value: "v"}),
		"UpdateKey":         c.UpdateKey(ctx, "up-a", Key{Value: "v"}),
		"DeleteKey":         c.DeleteKey(ctx, "up-a", ""),
		"UpdateVirtualKey":  c.UpdateVirtualKey(ctx, VirtualKey{Name: "n"}),
		"DeleteVirtualKey":  c.DeleteVirtualKey(ctx, ""),
		"UpdateRoutingRule": c.UpdateRoutingRule(ctx, RoutingRule{Name: "n"}),
		"DeleteRoutingRule": c.DeleteRoutingRule(ctx, ""),
	}
	for name, err := range calls {
		if err == nil || errors.Is(err, ErrNotFound) {
			t.Errorf("%s: got %v, want a non-NotFound error", name, err)
		}
	}
	if len(f.reqs) != 0 {
		t.Errorf("%d requests reached the engine", len(f.reqs))
	}
}

func TestClient_CreateRoutingRuleReturnsID(t *testing.T) {
	_, c := newFakeEngine(t, map[string]answer{
		"POST /api/routing/rules": {200, `{"message":"created","rule":{"id":"r1","name":"remora:alias:fast","cel_expression":"model == \"fast\"","targets":[{"provider":"up-a","model":"m1","weight":1}],"fallbacks":["up-b/m1"],"priority":0}}`},
	})
	got, err := c.CreateRoutingRule(context.Background(), RoutingRule{Name: "remora:alias:fast", CEL: `model == "fast"`, Target: Route{"up-a", "m1"}})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "r1" {
		t.Fatalf("ID %q, want r1", got.ID)
	}
	if got.Target != (Route{"up-a", "m1"}) || !reflect.DeepEqual(got.Fallbacks, []Route{{"up-b", "m1"}}) {
		t.Fatalf("decoded %+v", got)
	}
}

func TestClient_ListsDecode(t *testing.T) {
	_, c := newFakeEngine(t, map[string]answer{
		"GET /api/providers": {200, `{"providers":[
			{"name":"up-a","network_config":{"base_url":"u","default_request_timeout_in_seconds":1860,"allow_private_network":true},
			 "custom_provider_config":{"base_provider_type":"openai","allowed_requests":{"transcription":true,"chat_completion":false,"embedding":true}}},
			{"name":"openrouter","network_config":{"default_request_timeout_in_seconds":600}}],"total":2}`},
		"GET /api/providers/up-a/keys":     {200, `{"keys":[{"id":"k1","name":"main","value":{"value":"te****ey"},"models":["*"],"weight":1,"enabled":false},{"id":"k2","name":"spare","value":{"value":""}}],"total":2}`},
		"GET /api/governance/virtual-keys": {200, `{"virtual_keys":[{"id":"vk1","name":"remora:key:c1","value":"sk-bf-x","is_active":true},{"id":"vk2","name":"remora:key:c2","value":{"value":"sk-bf-y"},"is_active":false}],"count":2}`},
		"GET /api/routing/rules": {200, `{"rules":[
			{"id":"r1","name":"n","cel_expression":"c","priority":2,"targets":[{"provider":"up-a","model":"m1","weight":0.5},{"provider":"up-x","model":"m9","weight":0.5}],"fallbacks":["up-b/org/m2",{"provider":"up-c","model":"m3","key_id":"k9"}]},
			{"id":"r2","name":"o","cel_expression":"true","targets":[{"provider":"remora-oauth","weight":1}]}],"count":2}`},
	})
	ctx := context.Background()

	providers, err := c.ListProviders(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantProviders := []Provider{
		{
			Name: "up-a", BaseType: "openai", BaseURL: "u", AllowPrivateNetwork: true,
			AllowedRequests: []string{"embedding", "transcription"}, RequestTimeout: 1860 * time.Second,
		},
		{Name: "openrouter", RequestTimeout: 600 * time.Second},
	}
	if !reflect.DeepEqual(providers, wantProviders) {
		t.Errorf("providers %+v, want %+v", providers, wantProviders)
	}

	keys, err := c.ListKeys(ctx, "up-a")
	if err != nil {
		t.Fatal(err)
	}
	wantKeys := []Key{{ID: "k1", Name: "main", Enabled: false}, {ID: "k2", Name: "spare", Enabled: true}}
	if !reflect.DeepEqual(keys, wantKeys) {
		t.Errorf("keys %+v, want %+v", keys, wantKeys)
	}

	vks, err := c.ListVirtualKeys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []VirtualKey{
		{ID: "vk1", Name: "remora:key:c1", Value: "sk-bf-x", Active: true},
		{ID: "vk2", Name: "remora:key:c2", Value: "sk-bf-y"},
	}
	if !reflect.DeepEqual(vks, want) {
		t.Errorf("virtual keys %+v, want %+v", vks, want)
	}

	rules, err := c.ListRoutingRules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantRules := []RoutingRule{
		{
			ID: "r1", Name: "n", CEL: "c", Priority: 2, Target: Route{"up-a", "m1"}, Spread: []Route{{"up-x", "m9"}},
			Fallbacks: []Route{{"up-b", "org/m2"}, {"up-c", "m3"}},
		},
		{ID: "r2", Name: "o", CEL: "true", Target: Route{"remora-oauth", ""}},
	}
	if !reflect.DeepEqual(rules, wantRules) {
		t.Errorf("rules %+v, want %+v", rules, wantRules)
	}
}

func TestClient_Errors(t *testing.T) {
	_, c := newFakeEngine(t, map[string]answer{
		"DELETE /api/providers/gone":   {404, `{"is_bifrost_error":false,"status_code":404,"error":{"message":"Provider not found"}}`},
		"POST /api/providers":          {409, `{"error":"x","message":"Provider up-a already exists"}`},
		"POST /api/routing/rules":      {409, `{"is_bifrost_error":false,"status_code":409,"error":{"message":"priority taken"}}`},
		"GET /api/providers/up-a/keys": {500, `not json`},
	})
	ctx := context.Background()

	err := c.DeleteProvider(ctx, "gone")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("404: got %v, want ErrNotFound", err)
	}

	err = c.CreateProvider(ctx, Provider{Name: "up-a", BaseType: "openai"})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || *apiErr != (APIError{Status: 409, Message: "Provider up-a already exists"}) {
		t.Errorf("409: got %#v", err)
	}
	if errors.Is(err, ErrNotFound) {
		t.Error("409 must not be ErrNotFound")
	}

	_, err = c.CreateRoutingRule(ctx, RoutingRule{Name: "n", CEL: "true", Target: Route{"up-a", ""}})
	if !errors.As(err, &apiErr) || apiErr.Message != "priority taken" {
		t.Errorf("nested error message: got %#v", err)
	}

	_, err = c.ListKeys(ctx, "up-a")
	if !errors.As(err, &apiErr) || apiErr.Status != 500 || apiErr.Message == "" {
		t.Errorf("500: got %#v", err)
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := c.ListProviders(cancelled); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: got %v, want context.Canceled", err)
	}
}

func TestClient_Health(t *testing.T) {
	_, ok := newFakeEngine(t, map[string]answer{"GET /health": {200, `{"status":"ok","components":{}}`}})
	if err := ok.Health(context.Background()); err != nil {
		t.Errorf("200: %v", err)
	}
	_, down := newFakeEngine(t, map[string]answer{"GET /health": {503, `{"status":"error"}`}})
	if err := down.Health(context.Background()); err == nil {
		t.Error("503: want error")
	}
}

func TestIsStandardProvider(t *testing.T) {
	for name, want := range map[string]bool{
		"openrouter": true, "openai": true, "typesafe": true, "github-copilot": true, "bedrock_mantle": true,
		"up-a": false, OAuthProvider: false, "": false, "OpenAI": false,
	} {
		if got := IsStandardProvider(name); got != want {
			t.Errorf("IsStandardProvider(%q) = %v, want %v", name, got, want)
		}
	}
}
