package api

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/47vigen/remora/internal/engine/bifrost"
)

type logEntryJSON struct {
	ID       string  `json:"id"`
	ParentID *string `json:"parent_id"`
	Attempt  int     `json:"attempt"`
	Key      *string `json:"key"`
	Alias    *string `json:"alias"`
	Provider string  `json:"provider"`
	Model    string  `json:"model"`
	Status   string  `json:"status"`
	Error    *string `json:"error"`
}

type logDetailJSON struct {
	logEntryJSON
	Request      map[string]json.RawMessage `json:"request"`
	Response     map[string]json.RawMessage `json:"response"`
	FallbackPath []logEntryJSON             `json:"fallback_path"`
}

func TestListLogsFilters(t *testing.T) {
	env := newTestEnv(t)
	env.names.keys = map[string]string{"k1": "vk1"}
	env.names.rules = map[string]string{"fast": "r1"}
	from := time.Date(2026, 10, 14, 8, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)

	code, body := env.do(t, env.client, http.MethodGet, "/api/logs?key=k1&alias=fast&provider=or&model=or/a&status=error"+
		"&from="+from.Format(time.RFC3339)+"&to="+to.Format(time.RFC3339)+"&q=x", "")
	if code != http.StatusOK {
		t.Fatalf("list = %d %s", code, body)
	}
	f, n := env.logs.lastSearch()
	if n != 1 {
		t.Fatalf("searches = %d", n)
	}
	if !slices.Equal(f.VirtualKeyIDs, []string{"vk1"}) || !slices.Equal(f.RoutingRuleIDs, []string{"r1"}) ||
		!slices.Equal(f.Providers, []string{"fd-or"}) || !slices.Equal(f.Models, []string{"fd-or/a"}) ||
		!slices.Equal(f.Status, []string{"error"}) || f.Start == nil || !f.Start.Equal(from) || f.End == nil ||
		!f.End.Equal(to) || f.ContentSearch != "x" || f.SortBy != "timestamp" || f.Order != "desc" {
		t.Fatalf("filter = %+v", f)
	}
}

func TestListLogsUnknownAlias(t *testing.T) {
	env := newTestEnv(t)
	code, body := env.do(t, env.client, http.MethodGet, "/api/logs?alias=nope", "")
	if code != http.StatusOK || strings.TrimSpace(string(body)) != `{"items":[],"total":0}` {
		t.Fatalf("list = %d %s", code, body)
	}
	if _, n := env.logs.lastSearch(); n != 0 {
		t.Fatalf("searched %d times for an unknown alias", n)
	}
	if code, body := env.do(t, env.client, http.MethodGet, "/api/logs?key=nope", ""); code != http.StatusOK ||
		strings.TrimSpace(string(body)) != `{"items":[],"total":0}` {
		t.Fatalf("unknown key = %d %s", code, body)
	}
}

func TestListLogsPaging(t *testing.T) {
	env := newTestEnv(t)
	now := time.Now().UTC()
	for i := range 5 {
		env.logs.add(bifrost.Log{
			ID: string(rune('a' + i)), Timestamp: now.Add(time.Duration(i) * time.Second), Provider: "or", Model: "m",
			Status: "success",
		})
	}
	code, body := env.do(t, env.client, http.MethodGet, "/api/logs?limit=2&offset=1", "")
	if code != http.StatusOK {
		t.Fatalf("list = %d %s", code, body)
	}
	f, _ := env.logs.lastSearch()
	if f.Limit != 2 || f.Offset != 1 {
		t.Fatalf("filter paging = %d/%d", f.Limit, f.Offset)
	}
	var page struct {
		Items []logEntryJSON `json:"items"`
		Total int            `json:"total"`
	}
	decode(t, body, &page)
	if page.Total != 5 || len(page.Items) != 2 || page.Items[0].ID != "d" || page.Items[1].ID != "c" {
		t.Fatalf("page = %s", body)
	}

	if code, _ := env.do(t, env.client, http.MethodGet, "/api/logs", ""); code != http.StatusOK {
		t.Fatal(code)
	}
	if f, _ := env.logs.lastSearch(); f.Limit != 50 || f.Offset != 0 {
		t.Fatalf("default paging = %d/%d", f.Limit, f.Offset)
	}
	// Like every list, a limit above 200 is capped.
	if code, body := env.do(t, env.client, http.MethodGet, "/api/logs?limit=500", ""); code != http.StatusOK {
		t.Fatalf("limit 500 = %d %s", code, body)
	}
	if f, _ := env.logs.lastSearch(); f.Limit != 200 {
		t.Fatalf("capped limit = %d", f.Limit)
	}
}

// addFallbackChain adds a request r that failed 429 and fell back twice: c1 failed 402, c2 succeeded.
func addFallbackChain(env *testEnv) {
	env.names.keys = map[string]string{"k1": "vk1"}
	env.names.rules = map[string]string{"fast": "r1"}
	ts := time.Now().UTC()
	env.logs.add(
		bifrost.Log{
			ID: "r", Timestamp: ts, Provider: "or", Model: "a", Status: "error", VirtualKeyID: ptr("vk1"),
			RoutingRuleID: ptr("r1"), ErrorDetails: errDetails("rate limited by bifrost"),
			InputHistory: json.RawMessage(`[{"role":"user","content":"hi"}]`), Params: json.RawMessage(`{"temperature":0.2}`),
		},
		bifrost.Log{
			ID: "c1", ParentRequestID: ptr("r"), FallbackIndex: 1, Timestamp: ts.Add(time.Millisecond), Provider: "or",
			Model: "b", Status: "error", VirtualKeyID: ptr("vk1"), ErrorDetails: errDetails("payment required"),
		},
		bifrost.Log{
			ID: "c2", ParentRequestID: ptr("r"), FallbackIndex: 2, Timestamp: ts.Add(2 * time.Millisecond),
			Provider: bifrost.OAuthProvider, Model: "codex/gpt-x", Status: "success", VirtualKeyID: ptr("vk1"),
			OutputMessage: json.RawMessage(`{"role":"assistant","content":"yo"}`),
		},
	)
}

func getLogDetail(t *testing.T, env *testEnv, id string) (logDetailJSON, string) {
	t.Helper()
	code, body := env.do(t, env.client, http.MethodGet, "/api/logs/"+id, "")
	if code != http.StatusOK {
		t.Fatalf("get %s = %d %s", id, code, body)
	}
	var d logDetailJSON
	decode(t, body, &d)
	return d, string(body)
}

func checkFallbackPath(t *testing.T, d logDetailJSON) {
	t.Helper()
	want := []struct{ id, provider, model, status, err string }{
		{"r", "or", "a", "error", "rate limited by gateway"},
		{"c1", "or", "b", "error", "payment required"},
		{"c2", "codex", "gpt-x", "success", ""},
	}
	if len(d.FallbackPath) != len(want) {
		t.Fatalf("fallback_path = %+v", d.FallbackPath)
	}
	for i, w := range want {
		e := d.FallbackPath[i]
		if e.ID != w.id || e.Attempt != i || e.Provider != w.provider || e.Model != w.model || e.Status != w.status ||
			deref(e.Error) != w.err || deref(e.Alias) != "fast" || deref(e.Key) != "k1" {
			t.Errorf("fallback_path[%d] = %+v (error %q, alias %q), want %+v", i, e, deref(e.Error), deref(e.Alias), w)
		}
	}
}

func TestGetLogFallbackPath(t *testing.T) {
	env := newTestEnv(t)
	addFallbackChain(env)
	d, _ := getLogDetail(t, env, "r")
	if d.ID != "r" || d.Attempt != 0 || deref(d.Alias) != "fast" || deref(d.Error) != "rate limited by gateway" {
		t.Errorf("detail = %+v", d.logEntryJSON)
	}
	checkFallbackPath(t, d)
	f, _ := env.logs.lastSearch()
	if f.ParentRequestID != "r" || f.SortBy != "timestamp" || f.Order != "asc" || f.Limit != 50 {
		t.Errorf("path query = %+v", f)
	}

	if code, body := env.do(t, env.client, http.MethodGet, "/api/logs/gone", ""); code != http.StatusNotFound ||
		errCode(t, body) != "not_found" {
		t.Errorf("missing log = %d %s", code, body)
	}
}

func TestGetLogOfChildShowsWholePath(t *testing.T) {
	env := newTestEnv(t)
	addFallbackChain(env)
	d, _ := getLogDetail(t, env, "c1")
	if d.ID != "c1" || d.Attempt != 1 || deref(d.ParentID) != "r" || deref(d.Alias) != "fast" ||
		deref(d.Error) != "payment required" {
		t.Errorf("detail = %+v", d.logEntryJSON)
	}
	checkFallbackPath(t, d)
}

func TestGetLogBodies(t *testing.T) {
	env := newTestEnv(t)
	addFallbackChain(env)
	d, raw := getLogDetail(t, env, "r")
	if string(d.Request["messages"]) != `[{"role":"user","content":"hi"}]` || string(d.Request["params"]) != `{"temperature":0.2}` {
		t.Errorf("request = %s", raw)
	}
	if d.Response != nil {
		t.Errorf("response of a failed attempt = %v", d.Response)
	}
	d, raw = getLogDetail(t, env, "c2")
	if string(d.Response["message"]) != `{"role":"assistant","content":"yo"}` || d.Request != nil {
		t.Errorf("c2 bodies = %s", raw)
	}
	for _, field := range []string{"input_history", "output_message", "params\":{", "error_details", "fallback_index", "virtual_key_id"} {
		if strings.Contains(raw, `"`+field) {
			t.Errorf("detail carries engine field %s: %s", field, raw)
		}
	}
	assertNoEngineWords(t, "detail", raw)
}
