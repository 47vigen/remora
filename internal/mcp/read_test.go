package mcp

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestInspectListsAndShows(t *testing.T) {
	for _, tc := range []struct {
		args       map[string]any
		path, qry  string
		on         map[string]string
		wantFields []string
	}{
		{map[string]any{"kind": "providers", "q": "up"}, "/api/providers", "limit=50&q=up", nil, []string{"items"}},
		{map[string]any{"kind": "providers", "id": "up-a"}, "/api/providers/up-a", "", nil, []string{"name"}},
		{map[string]any{"kind": "accounts", "limit": 5, "offset": 10}, "/api/accounts", "limit=5&offset=10", nil, []string{"items"}},
		{map[string]any{"kind": "aliases"}, "/api/aliases", "limit=50", nil, []string{"items"}},
		{map[string]any{"kind": "aliases", "id": "fast"}, "/api/aliases/fast/chain", "", map[string]string{"GET /api/aliases/fast": `{"name":"fast","kind":"chat","members":["up-a/m1"],"capabilities":[]}`}, []string{"alias", "chain"}},
		{map[string]any{"kind": "keys"}, "/api/keys", "limit=50", nil, []string{"items"}},
		{map[string]any{"kind": "keys", "id": "k1"}, "/api/keys/k1/usage", "", map[string]string{"GET /api/keys/k1": `{"id":"k1","name":"full"}`}, []string{"key", "usage"}},
		{map[string]any{"kind": "tokens"}, "/api/settings/tokens", "limit=50", nil, []string{"items"}},
		{map[string]any{"kind": "cooling"}, "/api/cooling", "limit=50", nil, []string{"items"}},
		{map[string]any{"kind": "account_strategies"}, "/api/accounts/strategies", "limit=50", nil, []string{"items"}},
		{map[string]any{"kind": "logins", "id": "st-1"}, "/api/accounts/logins/st-1", "", nil, []string{"status"}},
	} {
		f := newFakeAPI(t)
		f.on("GET "+tc.path, http.StatusOK, `{"items":[],"total":0,"name":"x","status":"pending","usage":1}`)
		for p, b := range tc.on {
			f.on(p, http.StatusOK, b)
		}
		res, out := call(t, f.session(t), "remora_inspect", tc.args)
		if res.IsError {
			t.Errorf("%v: isError %s", tc.args, text(res))
			continue
		}
		reqs := f.requests()
		last := reqs[len(reqs)-1]
		if last.Path != tc.path || last.Query != tc.qry {
			t.Errorf("%v: last request %s?%s, want %s?%s", tc.args, last.Path, last.Query, tc.path, tc.qry)
		}
		for _, k := range tc.wantFields {
			if _, ok := out[k]; !ok {
				t.Errorf("%v: result %v lacks %q", tc.args, out, k)
			}
		}
	}
}

func TestInspectQuota(t *testing.T) {
	f := newFakeAPI(t)
	f.on("GET /api/quota", http.StatusOK, `[{"provider":"codex","accounts":[]},{"provider":"grok","accounts":[]}]`)
	cs := f.session(t)
	if _, out := call(t, cs, "remora_inspect", map[string]any{"kind": "quota"}); len(out["items"].([]any)) != 2 {
		t.Errorf("quota list = %v", out)
	}
	if _, out := call(t, cs, "remora_inspect", map[string]any{"kind": "quota", "id": "grok"}); out["provider"] != "grok" {
		t.Errorf("quota grok = %v", out)
	}
	if res, _ := call(t, cs, "remora_inspect", map[string]any{"kind": "quota", "id": "nope"}); !res.IsError {
		t.Error("unknown quota provider is not an error")
	}
}

func TestInspectRefusesIDWhereListedOnly(t *testing.T) {
	f := newFakeAPI(t)
	cs := f.session(t)
	for _, kind := range []string{"tokens", "cooling", "account_strategies"} {
		if res, _ := call(t, cs, "remora_inspect", map[string]any{"kind": kind, "id": "x"}); !res.IsError || !strings.Contains(text(res), "listed only") {
			t.Errorf("%s+id = %s", kind, text(res))
		}
	}
	if res, _ := call(t, cs, "remora_inspect", map[string]any{"kind": "logins"}); !res.IsError || !strings.Contains(text(res), "login state is required") {
		t.Errorf("logins without id = %s", text(res))
	}
	if n := len(f.requests()); n != 0 {
		t.Errorf("%d requests made", n)
	}
}

func TestInspectUnknownKind(t *testing.T) {
	res, _ := call(t, newFakeAPI(t).session(t), "remora_inspect", map[string]any{"kind": "users"})
	if !res.IsError || !strings.Contains(text(res), "account_strategies") {
		t.Errorf("unknown kind = %s", text(res))
	}
}

func TestInspectMissingAccount(t *testing.T) {
	f := newFakeAPI(t)
	f.on("GET /api/accounts", http.StatusOK, `{"items":[{"id":"a1","enabled":true,"models":[]}],"total":1}`)
	cs := f.session(t)
	if res, _ := call(t, cs, "remora_inspect", map[string]any{"kind": "accounts", "id": "a9"}); !res.IsError || !strings.Contains(text(res), "No account a9.") {
		t.Errorf("missing account = %s", text(res))
	}
	if res, out := call(t, cs, "remora_inspect", map[string]any{"kind": "accounts", "id": "a1"}); res.IsError || out["id"] != "a1" {
		t.Errorf("account a1 = %v %s", out, text(res))
	}
}

func TestLogsSearchPassesFilters(t *testing.T) {
	f := newFakeAPI(t)
	f.on("GET /api/logs", http.StatusOK, `{"items":[],"total":0}`)
	res, _ := call(t, f.session(t), "remora_logs", map[string]any{
		"from": "2026-10-16T00:00:00Z", "to": "2026-10-16T12:00:00Z", "provider": "up-a", "status": "error",
	})
	if res.IsError {
		t.Fatal(text(res))
	}
	q := f.requests()[0].Query
	for _, want := range []string{"from=2026-10-16T00%3A00%3A00Z", "to=2026-10-16T12%3A00%3A00Z", "provider=up-a", "status=error", "limit=20"} {
		if !strings.Contains(q, want) {
			t.Errorf("query %q lacks %q", q, want)
		}
	}
}

func TestLogsTruncatesBodies(t *testing.T) {
	big := strings.Repeat("x", 1<<20)
	b, _ := json.Marshal(map[string]any{
		"id": "l1", "request": map[string]any{"messages": big}, "response": map[string]any{"ok": strings.Repeat("y", 80)},
	})
	f := newFakeAPI(t)
	f.on("GET /api/logs/l1", http.StatusOK, string(b))
	res, out := call(t, f.session(t), "remora_logs", map[string]any{"id": "l1"})
	if res.IsError {
		t.Fatal(text(res))
	}
	req, _ := out["request"].(map[string]any)
	full, _ := json.Marshal(map[string]any{"messages": big})
	if req["truncated"] != true || req["bytes"] != float64(len(full)) || len(req["head"].(string)) != maxBody {
		t.Errorf("request = truncated %v bytes %v head %d", req["truncated"], req["bytes"], len(req["head"].(string)))
	}
	if resp, _ := out["response"].(map[string]any); resp["ok"] != strings.Repeat("y", 80) {
		t.Errorf("response = %v, want it unchanged", resp)
	}
}

func TestLogsBadTime(t *testing.T) {
	f := newFakeAPI(t)
	res, _ := call(t, f.session(t), "remora_logs", map[string]any{"from": "yesterday"})
	if !res.IsError || !strings.Contains(text(res), "from") || len(f.requests()) != 0 {
		t.Errorf("bad time = %s, %d requests", text(res), len(f.requests()))
	}
}

func TestStatsGroupBy(t *testing.T) {
	f := newFakeAPI(t)
	f.on("GET /api/stats", http.StatusOK, `{"totals":{}}`)
	if res, _ := call(t, f.session(t), "remora_stats", map[string]any{"group_by": "alias"}); res.IsError {
		t.Fatal(text(res))
	}
	if q := f.requests()[0].Query; q != "group_by=alias" {
		t.Errorf("query %q", q)
	}
}

func TestAliasCompile(t *testing.T) {
	f := newFakeAPI(t)
	f.on("POST /api/aliases/compile", http.StatusOK, `{"targets":[]}`)
	in := map[string]any{"name": "fast", "kind": "chat", "members": []any{"up-a/m1"}, "capabilities": []any{}}
	if res, _ := call(t, f.session(t), "remora_alias_compile", in); res.IsError {
		t.Fatal(text(res))
	}
	var got map[string]any
	_ = json.Unmarshal([]byte(f.requests()[0].Body), &got)
	if got["name"] != "fast" || got["kind"] != "chat" || len(got["members"].([]any)) != 1 {
		t.Errorf("body %s", f.requests()[0].Body)
	}
}

func TestProviderTest(t *testing.T) {
	f := newFakeAPI(t)
	f.on("POST /api/providers/up-a/test", http.StatusOK, `{"ok":true,"latency_ms":12,"message":""}`)
	cs := f.session(t)
	if res, out := call(t, cs, "remora_provider_test", map[string]any{"name": "up-a"}); res.IsError || out["ok"] != true {
		t.Fatalf("%v %s", out, text(res))
	}
	tools, _ := cs.ListTools(t.Context(), nil)
	for _, tool := range tools.Tools {
		if tool.Name == "remora_provider_test" && (!strings.Contains(tool.Description, "real request") || tool.Annotations.IdempotentHint || !*tool.Annotations.OpenWorldHint) {
			t.Errorf("provider test tool = %q %+v", tool.Description, tool.Annotations)
		}
	}
}

func TestConfigExport(t *testing.T) {
	doc := "# remora.yaml\nversion: 1\nproviders: []\naliases: []\nkeys: []\n"
	f := newFakeAPI(t)
	f.onType("GET /api/config/export", http.StatusOK, "application/yaml", doc)
	if _, out := call(t, f.session(t), "remora_config_export", map[string]any{}); out["document"] != doc {
		t.Errorf("document = %q", out["document"])
	}
}
