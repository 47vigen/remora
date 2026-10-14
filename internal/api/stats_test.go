package api

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/47vigen/remora/internal/alias"
	"github.com/47vigen/remora/internal/engine/bifrost"
)

type statsJSON struct {
	From    time.Time        `json:"from"`
	To      time.Time        `json:"to"`
	Summary map[string]any   `json:"summary"`
	Series  []map[string]any `json:"series"`
	Groups  []statsGroupJSON `json:"groups"`
}

type statsGroupJSON struct {
	Name    string         `json:"name"`
	Summary map[string]any `json:"summary"`
}

func getStats(t *testing.T, env *testEnv, path string) (statsJSON, string) {
	t.Helper()
	code, body := env.do(t, env.client, http.MethodGet, path, "")
	if code != http.StatusOK {
		t.Fatalf("GET %s = %d %s", path, code, body)
	}
	var s statsJSON
	decode(t, body, &s)
	return s, string(body)
}

func TestPercentile(t *testing.T) {
	logs := &fakeLogs{}
	ts := time.Now()
	for i := 10; i >= 1; i-- {
		logs.add(bifrost.Log{ID: string(rune('a' + i)), Timestamp: ts, Status: "success", Latency: ptr(float64(i * 10))})
	}
	logs.add(bifrost.Log{ID: "slow-error", Timestamp: ts, Status: "error", Latency: ptr(5000.0)})
	ctx := context.Background()

	p50, err := percentile(ctx, logs, bifrost.LogFilter{}, 0.5)
	if err != nil || p50 == nil || *p50 != 50 {
		t.Fatalf("p50 = %v, %v", p50, err)
	}
	f, _ := logs.lastSearch()
	if f.Offset != 4 || f.Limit != 1 || f.SortBy != "latency" || f.Order != "asc" || !slices.Equal(f.Status, []string{"success"}) {
		t.Errorf("p50 query = %+v", f)
	}
	p95, err := percentile(ctx, logs, bifrost.LogFilter{}, 0.95)
	if err != nil || p95 == nil || *p95 != 100 {
		t.Fatalf("p95 = %v, %v", p95, err)
	}
	if f, _ := logs.lastSearch(); f.Offset != 9 {
		t.Errorf("p95 offset = %d", f.Offset)
	}

	none, err := percentile(ctx, &fakeLogs{}, bifrost.LogFilter{}, 0.5)
	if err != nil || none != nil {
		t.Fatalf("empty p50 = %v, %v", none, err)
	}
}

func TestStatsEmptyRange(t *testing.T) {
	env := newTestEnv(t)
	s, raw := getStats(t, env, "/api/stats")
	if strings.Contains(raw, "NaN") || strings.Contains(raw, "Inf") {
		t.Fatalf("body = %s", raw)
	}
	for _, k := range []string{"requests", "errors", "error_rate", "prompt_tokens", "completion_tokens", "total_tokens", "cost_usd"} {
		if v, ok := s.Summary[k]; !ok || v != 0.0 {
			t.Errorf("summary.%s = %v (present %v), want 0", k, v, ok)
		}
	}
	if s.Summary["latency_p50_ms"] != nil || s.Summary["latency_p95_ms"] != nil {
		t.Errorf("percentiles = %v, %v; want null", s.Summary["latency_p50_ms"], s.Summary["latency_p95_ms"])
	}
	if s.Series == nil || s.Groups == nil {
		t.Errorf("series or groups is null: %s", raw)
	}
}

func TestStatsSummary(t *testing.T) {
	env := newTestEnv(t)
	env.logs.stats = func(bifrost.LogFilter) bifrost.LogStats {
		return bifrost.LogStats{
			UserFacingTotalRequests: 200, UserFacingSuccessRate: 97.5, PromptTokens: 600, CompletionTokens: 300,
			TotalTokens: 900, TotalCost: 1.75,
		}
	}
	ts := time.Now().UTC().Truncate(time.Hour)
	env.logs.histogram = []bifrost.HistogramBucket{{Timestamp: ts, Count: 7, Success: 5, Error: 2}}
	env.logs.add(bifrost.Log{ID: "a", Timestamp: ts, Status: "success", Latency: ptr(42.0)})
	s, raw := getStats(t, env, "/api/stats")
	want := map[string]any{
		"requests": 200.0, "errors": 5.0, "error_rate": 0.025, "prompt_tokens": 600.0, "completion_tokens": 300.0,
		"total_tokens": 900.0, "cost_usd": 1.75, "latency_p50_ms": 42.0, "latency_p95_ms": 42.0,
	}
	for k, v := range want {
		if s.Summary[k] != v {
			t.Errorf("summary.%s = %v, want %v", k, s.Summary[k], v)
		}
	}
	if len(s.Series) != 1 || s.Series[0]["requests"] != 7.0 || s.Series[0]["errors"] != 2.0 ||
		s.Series[0]["time"] != ts.Format(time.RFC3339) {
		t.Errorf("series = %s", raw)
	}
	if len(s.Groups) != 0 {
		t.Errorf("groups without group_by = %v", s.Groups)
	}
}

func TestStatsGroupByAlias(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	for _, name := range []string{"fast", "smart", "idle"} {
		if _, err := env.aliases.Put(ctx, alias.Alias{Name: name}, true); err != nil {
			t.Fatal(err)
		}
	}
	env.names.rules = map[string]string{"fast": "r1", "smart": "r2", "idle": "r3"}
	env.logs.stats = func(f bifrost.LogFilter) bifrost.LogStats {
		switch {
		case slices.Equal(f.RoutingRuleIDs, []string{"r1"}):
			return bifrost.LogStats{UserFacingTotalRequests: 10, UserFacingSuccessRate: 100}
		case slices.Equal(f.RoutingRuleIDs, []string{"r2"}):
			return bifrost.LogStats{UserFacingTotalRequests: 4, UserFacingSuccessRate: 50}
		case len(f.RoutingRuleIDs) == 0:
			return bifrost.LogStats{UserFacingTotalRequests: 14, UserFacingSuccessRate: 85.7}
		}
		return bifrost.LogStats{}
	}
	s, raw := getStats(t, env, "/api/stats?group_by=alias")
	var rules [][]string
	for _, f := range env.logs.statsCalls {
		rules = append(rules, f.RoutingRuleIDs)
	}
	for _, want := range [][]string{{"r1"}, {"r2"}, {"r3"}} {
		if !slices.ContainsFunc(rules, func(r []string) bool { return slices.Equal(r, want) }) {
			t.Errorf("no stats call for %v: %v", want, rules)
		}
	}
	if len(s.Groups) != 2 || s.Groups[0].Name != "fast" || s.Groups[1].Name != "smart" ||
		s.Groups[0].Summary["requests"] != 10.0 || s.Groups[1].Summary["errors"] != 2.0 {
		t.Fatalf("groups = %s", raw)
	}
	if s.Summary["requests"] != 14.0 {
		t.Errorf("summary = %v", s.Summary)
	}
}

func TestStatsGroupByProviderAndKey(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	if _, err := env.providers.Create(ctx, Provider{Name: "or", Kind: "builtin"}); err != nil {
		t.Fatal(err)
	}
	k, _, err := env.keys.Create(ctx, "svc", nil, "", true)
	if err != nil {
		t.Fatal(err)
	}
	env.names.keys = map[string]string{k.ID: "vk1"}
	env.logs.stats = func(f bifrost.LogFilter) bifrost.LogStats {
		if len(f.Providers) > 0 || len(f.VirtualKeyIDs) > 0 {
			return bifrost.LogStats{UserFacingTotalRequests: 3, UserFacingSuccessRate: 100}
		}
		return bifrost.LogStats{}
	}
	s, raw := getStats(t, env, "/api/stats?group_by=provider")
	if len(s.Groups) != 1 || s.Groups[0].Name != "or" {
		t.Fatalf("provider groups = %s", raw)
	}
	if f := env.logs.statsCalls[len(env.logs.statsCalls)-1]; !slices.Equal(f.Providers, []string{"fd-or"}) {
		t.Errorf("provider filter = %v", f.Providers)
	}
	s, raw = getStats(t, env, "/api/stats?group_by=key")
	if len(s.Groups) != 1 || s.Groups[0].Name != k.ID {
		t.Fatalf("key groups = %s", raw)
	}
}

func TestStatsDefaultRange(t *testing.T) {
	env := newTestEnv(t)
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	env.server.now = func() time.Time { return now }
	s, _ := getStats(t, env, "/api/stats")
	if !s.To.Equal(now) || !s.From.Equal(now.Add(-24*time.Hour)) {
		t.Fatalf("range = %v..%v", s.From, s.To)
	}
	f := env.logs.statsCalls[0]
	if f.Start == nil || f.End == nil || !f.Start.Equal(s.From) || !f.End.Equal(s.To) {
		t.Fatalf("stats filter = %+v", f)
	}
	if h := env.logs.histCalls[0]; h.Start == nil || !h.Start.Equal(s.From) {
		t.Fatalf("histogram filter = %+v", h)
	}
}

func TestKeyUsage(t *testing.T) {
	env := newTestEnv(t)
	k, _, err := env.keys.Create(context.Background(), "svc", nil, "", true)
	if err != nil {
		t.Fatal(err)
	}
	env.names.keys = map[string]string{k.ID: "vk1"}
	env.logs.stats = func(bifrost.LogFilter) bifrost.LogStats {
		return bifrost.LogStats{UserFacingTotalRequests: 8, UserFacingSuccessRate: 75}
	}
	s, _ := getStats(t, env, "/api/keys/"+k.ID+"/usage")
	if f := env.logs.statsCalls[0]; !slices.Equal(f.VirtualKeyIDs, []string{"vk1"}) {
		t.Fatalf("usage filter = %+v", f)
	}
	if s.Summary["requests"] != 8.0 || s.Summary["errors"] != 2.0 || len(s.Groups) != 0 {
		t.Errorf("usage = %+v", s)
	}
	if code, body := env.do(t, env.client, http.MethodGet, "/api/keys/nope/usage", ""); code != http.StatusNotFound ||
		errCode(t, body) != "not_found" {
		t.Errorf("unknown key = %d %s", code, body)
	}
}
