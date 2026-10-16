package mcp

import (
	"net/http"
	"testing"
)

func TestOverview(t *testing.T) {
	f := newFakeAPI(t)
	f.on("GET /api/health", http.StatusOK, `{"status":"ok","engines":{"router":"ok","accounts":"ok"}}`)
	f.on("GET /api/system/version", http.StatusOK, `{"remora":"1.2.3","engines":[{"name":"x","version":"9"}]}`)
	f.on("GET /api/providers", http.StatusOK, `{"items":[],"total":3}`)
	f.on("GET /api/aliases", http.StatusOK, `{"items":[],"total":5}`)
	f.on("GET /api/keys", http.StatusOK, `{"items":[],"total":2}`)
	f.on("GET /api/accounts", http.StatusOK, `{"items":[],"total":20}`)
	f.on("GET /api/accounts/pool", http.StatusOK, `[{"provider":"codex","enabled":3,"out":[{"id":"a1","reason":"cooling"}]},{"provider":"grok","enabled":1,"out":[]}]`)
	f.on("GET /api/cooling", http.StatusOK, `{"items":[],"total":1}`)
	f.on("GET /api/quota", http.StatusOK, `[{"provider":"codex","supported":true,"available":2,"cooling":1,"exhausted":0,"sign_in":0,"accounts":[{"account":{}}]}]`)

	res, out := call(t, f.session(t), "remora_overview", map[string]any{})
	if res.IsError {
		t.Fatalf("isError: %s", text(res))
	}
	for k, want := range map[string]float64{"providers": 3, "aliases": 5, "keys": 2, "accounts": 20, "cooling": 1} {
		if out[k] != want {
			t.Errorf("%s = %v, want %v", k, out[k], want)
		}
	}
	if out["status"] != "ok" || out["version"] != "1.2.3" {
		t.Errorf("status %v version %v", out["status"], out["version"])
	}
	if pool, _ := out["out_of_pool"].([]any); len(pool) != 1 {
		t.Errorf("out_of_pool = %v", out["out_of_pool"])
	}
	quota, _ := out["quota"].([]any)
	if len(quota) != 1 {
		t.Fatalf("quota = %v", out["quota"])
	}
	if q := quota[0].(map[string]any); q["provider"] != "codex" || q["accounts"] != nil {
		t.Errorf("quota[0] = %v, want the header without accounts", q)
	}
	for _, r := range f.requests() {
		if r.Path == "/api/providers" && r.Query != "limit=1" {
			t.Errorf("providers query %q", r.Query)
		}
	}
}
