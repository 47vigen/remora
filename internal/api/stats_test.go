package api

import (
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/47vigen/remora/internal/alias"
	"github.com/47vigen/remora/internal/engine/bifrost"
	"github.com/47vigen/remora/internal/stats"
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
	Label   *string        `json:"label"`
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

func TestStatsNeverCallsEngine(t *testing.T) {
	var calls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(upstream.Close)
	ctx := t.Context()
	keys := &fakeKeys{}
	key, _, err := keys.Create(ctx, "svc", nil, "", true)
	if err != nil {
		t.Fatal(err)
	}
	aliases := &fakeAliases{}
	if _, err := aliases.Put(ctx, alias.Alias{Name: "fast"}, true); err != nil {
		t.Fatal(err)
	}
	providers := &fakeProviders{}
	if _, err := providers.Create(ctx, Provider{Name: "or", Kind: "builtin"}); err != nil {
		t.Fatal(err)
	}
	rollup := openStats(t)
	at := time.Now().UTC().Truncate(time.Minute)
	ingestStats(t, rollup,
		bifrost.Log{Timestamp: at, Status: "success", Provider: "fd-or", Model: "m", VirtualKeyID: new("vk"), RoutingRuleID: new("rule"), Latency: new(20.0)},
		bifrost.Log{Timestamp: at, Status: "success", Provider: "fd-codex", Model: "codex/c-1", VirtualKeyID: new("vk"), RoutingRuleID: new("rule"), Latency: new(30.0)},
	)
	s := New(Deps{
		Stats: rollup,
		Logs:  bifrost.New(upstream.URL, "", "", upstream.Client()), Keys: keys, Aliases: aliases,
		Providers: providers, Accounts: &fakeAccounts{accounts: []Account{{ID: "1", Provider: "codex"}}},
		Names: &fakeNames{keys: map[string]string{key.ID: "vk"}, rules: map[string]string{"fast": "rule"}},
	})
	h := s.Handler()
	for _, group := range []string{"none", "key", "alias", "provider", "model"} {
		for _, filters := range []string{"", "&key=" + key.ID, "&alias=fast", "&provider=or", "&provider=codex", "&key=" + key.ID + "&alias=fast&provider=codex"} {
			for _, groupsOnly := range []string{"", "&groups_only=true"} {
				path := "/api/stats?group_by=" + group + filters + groupsOnly
				w := httptest.NewRecorder()
				h.ServeHTTP(w, httptest.NewRequestWithContext(ctx, http.MethodGet, path, nil))
				if w.Code != http.StatusOK {
					t.Errorf("GET %s = %d %s", path, w.Code, w.Body)
				}
			}
		}
	}
	path := "/api/keys/" + key.ID + "/usage"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequestWithContext(ctx, http.MethodGet, path, nil))
	if w.Code != http.StatusOK {
		t.Errorf("GET %s = %d %s", path, w.Code, w.Body)
	}
	if n := calls.Load(); n != 0 {
		t.Fatalf("engine reads = %d, want 0", n)
	}
}

func openStats(t *testing.T) *stats.Store {
	t.Helper()
	s, err := stats.Open(t.Context(), filepath.Join(t.TempDir(), "stats.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func ingestStats(t *testing.T, s *stats.Store, rows ...bifrost.Log) {
	t.Helper()
	for i := range rows {
		rows[i].ID = strconv.Itoa(i)
	}
	if err := s.Ingest(t.Context(), rows); err != nil {
		t.Fatal(err)
	}
}

func newStatsEnv(t *testing.T, rows ...bifrost.Log) *testEnv {
	t.Helper()
	env := newTestEnv(t)
	s := openStats(t)
	ingestStats(t, s, rows...)
	env.server.d.Stats = s
	return env
}

func checkStatsLatency(t *testing.T, got any, exact float64) {
	t.Helper()
	if ms, ok := got.(float64); !ok || math.Abs(ms/exact-1) > .1 {
		t.Errorf("latency = %v, want within 10%% of %v ms", got, exact)
	}
}

func TestStatsUnavailable(t *testing.T) {
	env := newTestEnv(t)
	k, _, err := env.keys.Create(t.Context(), "svc", nil, "", true)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/stats", "/api/keys/" + k.ID + "/usage"} {
		code, body := env.do(t, env.client, http.MethodGet, path, "")
		if code != http.StatusServiceUnavailable || errCode(t, body) != "stats_unavailable" ||
			!strings.Contains(string(body), "Request stats are not available yet.") {
			t.Errorf("GET %s = %d %s, want 503 stats_unavailable", path, code, body)
		}
	}
}

func TestStatsBucketSize(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if got := statsBucketSize(&start, nil); got != 3600 {
		t.Errorf("missing end: got %d, want 3600", got)
	}
	if got := statsBucketSize(nil, &start); got != 3600 {
		t.Errorf("missing start: got %d, want 3600", got)
	}
	const day = 24 * time.Hour
	for _, tt := range []struct {
		duration time.Duration
		want     int64
	}{
		{0, 60},
		{2*time.Hour - time.Nanosecond, 60},
		{2 * time.Hour, 600},
		{day - time.Nanosecond, 600},
		{day, 3600},
		{3*day - time.Nanosecond, 3600},
		{3 * day, 8 * 3600},
		{7*day - time.Nanosecond, 8 * 3600},
		{7 * day, 24 * 3600},
		{31 * day, 24 * 3600},
		{31*day + time.Nanosecond, 3 * 24 * 3600},
		{90*day - time.Nanosecond, 3 * 24 * 3600},
		{90 * day, 7 * 24 * 3600},
		{365*day - time.Nanosecond, 7 * 24 * 3600},
		{365 * day, 30 * 24 * 3600},
	} {
		end := start.Add(tt.duration)
		if got := statsBucketSize(&start, &end); got != tt.want {
			t.Errorf("range %s = %ds, want %ds", tt.duration, got, tt.want)
		}
	}
}

func TestStatsEmptyRange(t *testing.T) {
	env := newStatsEnv(t)
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
	ts := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	env := newStatsEnv(t,
		bifrost.Log{Timestamp: ts, Status: "success", Latency: new(30.0), Cost: new(0.5), TokenUsage: &bifrost.TokenUsage{PromptTokens: 200, CompletionTokens: 100, TotalTokens: 300}},
		bifrost.Log{Timestamp: ts, Status: "success", Latency: new(10.0), Cost: new(0.75), TokenUsage: &bifrost.TokenUsage{PromptTokens: 200, CompletionTokens: 100, TotalTokens: 300}},
		bifrost.Log{Timestamp: ts, Status: "success", Latency: new(20.0), Cost: new(0.5), TokenUsage: &bifrost.TokenUsage{PromptTokens: 200, CompletionTokens: 100, TotalTokens: 300}},
		bifrost.Log{Timestamp: ts, Status: "error"},
	)
	env.server.now = func() time.Time { return ts.Add(45 * time.Second) }
	s, raw := getStats(t, env, "/api/stats?from="+ts.Format(time.RFC3339))
	want := map[string]any{
		"requests": 4.0, "errors": 1.0, "error_rate": 0.25, "prompt_tokens": 600.0, "completion_tokens": 300.0,
		"total_tokens": 900.0, "cost_usd": 1.75,
	}
	for k, v := range want {
		if s.Summary[k] != v {
			t.Errorf("summary.%s = %v, want %v", k, s.Summary[k], v)
		}
	}
	checkStatsLatency(t, s.Summary["latency_p50_ms"], 20)
	checkStatsLatency(t, s.Summary["latency_p95_ms"], 30)
	if len(s.Series) != 1 || s.Series[0]["requests"] != s.Summary["requests"] || s.Series[0]["errors"] != s.Summary["errors"] ||
		s.Series[0]["time"] != ts.Format(time.RFC3339) {
		t.Errorf("series = %s", raw)
	}
	if len(s.Groups) != 0 {
		t.Errorf("groups without group_by = %v", s.Groups)
	}
}

func TestStatsSingleBucketP95MatchesSummary(t *testing.T) {
	ts := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	var rows []bifrost.Log
	for range 15 {
		rows = append(rows, bifrost.Log{Timestamp: ts, Status: "success", Latency: new(10.0)})
	}
	env := newStatsEnv(t, append(rows,
		bifrost.Log{Timestamp: ts, Status: "success", Latency: new(4520.0), Stream: true},
		bifrost.Log{Timestamp: ts, Status: "error", Latency: new(9000.0)},
	)...)
	env.server.now = func() time.Time { return ts.Add(45 * time.Second) }

	s, raw := getStats(t, env, "/api/stats?from="+ts.Format(time.RFC3339))
	if len(s.Series) != 1 {
		t.Fatalf("expected one bucket: %s", raw)
	}
	checkStatsLatency(t, s.Summary["latency_p95_ms"], 4520)
	if summary, bucket := s.Summary["latency_p95_ms"], s.Series[0]["latency_p95_ms"]; bucket != summary {
		t.Errorf("window p95 = %v ms, bucket p95 = %v ms; want them to match", summary, bucket)
	}
}

func TestStatsSeriesSumsToSummary(t *testing.T) {
	ts := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	var rows []bifrost.Log
	for i := range 7 {
		row := bifrost.Log{Timestamp: ts, Status: "success", Cost: new(0.25), TokenUsage: &bifrost.TokenUsage{PromptTokens: 2, CompletionTokens: 1, TotalTokens: 3}}
		if i < 2 {
			row.Timestamp = ts.Add(-time.Hour)
			row.Status = "error"
		}
		rows = append(rows, row)
	}
	env := newStatsEnv(t, rows...)
	env.server.now = func() time.Time { return ts }
	s, _ := getStats(t, env, "/api/stats")
	if len(s.Series) != 25 || s.Series[23]["requests"] != 2.0 || s.Series[24]["requests"] != 5.0 || s.Summary["requests"] != 7.0 {
		t.Fatalf("unexpected series or summary: %+v", s)
	}
	for _, k := range []string{"requests", "errors", "prompt_tokens", "completion_tokens", "total_tokens", "cost_usd"} {
		var sum float64
		for _, point := range s.Series {
			sum += point[k].(float64)
		}
		if sum != s.Summary[k] {
			t.Errorf("series %s = %v, summary = %v", k, sum, s.Summary[k])
		}
	}
}

// A caller that holds the summary and the series already asks for the groups alone.
func TestStatsGroupsOnly(t *testing.T) {
	var rows []bifrost.Log
	ts := time.Now().UTC()
	for range 4 {
		rows = append(rows, bifrost.Log{Timestamp: ts, Status: "success", Provider: "fd-or", Model: "gpt-x"})
	}
	env := newStatsEnv(t, rows...)
	env.names.targets = map[[2]string]alias.Target{{"fd-or", "gpt-x"}: {Provider: "or", Model: "gpt-x"}}
	s, raw := getStats(t, env, "/api/stats?group_by=model&groups_only=true")
	if len(s.Groups) != 1 || s.Groups[0].Summary["requests"] != 4.0 {
		t.Fatalf("groups = %s", raw)
	}
	if s.Summary["requests"] != 0.0 || len(s.Series) != 0 {
		t.Errorf("summary or series included: %s", raw)
	}
}

func TestStatsGroupByAlias(t *testing.T) {
	ts := time.Now().UTC()
	var rows []bifrost.Log
	for i := range 14 {
		row := bifrost.Log{Timestamp: ts, Status: "success", RoutingRuleID: new("r1")}
		if i >= 10 {
			row.RoutingRuleID = new("r2")
		}
		if i >= 12 {
			row.Status = "error"
		}
		rows = append(rows, row)
	}
	env := newStatsEnv(t, rows...)
	for _, name := range []string{"fast", "smart", "idle"} {
		if _, err := env.aliases.Put(t.Context(), alias.Alias{Name: name}, true); err != nil {
			t.Fatal(err)
		}
	}
	env.names.rules = map[string]string{"fast": "r1", "smart": "r2", "idle": "r3"}
	s, raw := getStats(t, env, "/api/stats?group_by=alias")
	if len(s.Groups) != 2 || s.Groups[0].Name != "fast" || s.Groups[1].Name != "smart" ||
		s.Groups[0].Summary["requests"] != 10.0 || s.Groups[1].Summary["errors"] != 2.0 {
		t.Fatalf("groups = %s", raw)
	}
	if s.Summary["requests"] != 14.0 {
		t.Errorf("summary = %v", s.Summary)
	}
	s, raw = getStats(t, env, "/api/stats?group_by=alias&alias=smart")
	if len(s.Groups) != 1 || s.Groups[0].Name != "smart" || s.Summary["requests"] != 4.0 {
		t.Errorf("scoped stats = %s", raw)
	}
}

func TestStatsGroupByProviderAndKey(t *testing.T) {
	ts := time.Now().UTC()
	row := bifrost.Log{Timestamp: ts, Status: "success", Provider: "fd-or", VirtualKeyID: new("vk1")}
	env := newStatsEnv(t, row, row, row, bifrost.Log{Timestamp: ts, Status: "success", Provider: "fd-other", VirtualKeyID: new("vk2")})
	if _, err := env.providers.Create(t.Context(), Provider{Name: "or", Kind: "builtin"}); err != nil {
		t.Fatal(err)
	}
	if _, err := env.providers.Create(t.Context(), Provider{Name: "other", Kind: "builtin"}); err != nil {
		t.Fatal(err)
	}
	k, _, err := env.keys.Create(t.Context(), "svc", nil, "", true)
	if err != nil {
		t.Fatal(err)
	}
	env.names.keys = map[string]string{k.ID: "vk1"}
	s, raw := getStats(t, env, "/api/stats?group_by=provider&key="+k.ID)
	if len(s.Groups) != 1 || s.Groups[0].Name != "or" || s.Groups[0].Label != nil || s.Summary["requests"] != 3.0 {
		t.Fatalf("provider groups = %s", raw)
	}
	s, raw = getStats(t, env, "/api/stats?group_by=key&provider=or")
	if len(s.Groups) != 1 || s.Groups[0].Name != k.ID || s.Groups[0].Label == nil || *s.Groups[0].Label != "svc" ||
		s.Summary["requests"] != 3.0 {
		t.Fatalf("key groups = %s", raw)
	}
}

func TestStatsDefaultRange(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	env := newStatsEnv(t, bifrost.Log{Timestamp: now.Add(-24*time.Hour - time.Minute), Status: "success"},
		bifrost.Log{Timestamp: now.Add(-24 * time.Hour), Status: "success"})
	env.server.now = func() time.Time { return now }
	s, _ := getStats(t, env, "/api/stats")
	if !s.To.Equal(now) || !s.From.Equal(now.Add(-24*time.Hour)) {
		t.Fatalf("range = %v..%v", s.From, s.To)
	}
	if s.Summary["requests"] != 1.0 || len(s.Series) == 0 || s.Series[0]["requests"] != 1.0 {
		t.Errorf("unexpected default range totals: %+v", s)
	}
}

func TestKeyUsage(t *testing.T) {
	ts := time.Now().UTC()
	var rows []bifrost.Log
	for i := range 8 {
		row := bifrost.Log{Timestamp: ts, Status: "success", VirtualKeyID: new("vk1")}
		if i >= 6 {
			row.Status = "error"
		}
		rows = append(rows, row)
	}
	env := newStatsEnv(t, append(rows, bifrost.Log{Timestamp: ts, Status: "success", VirtualKeyID: new("vk2")})...)
	k, _, err := env.keys.Create(t.Context(), "svc", nil, "", true)
	if err != nil {
		t.Fatal(err)
	}
	env.names.keys = map[string]string{k.ID: "vk1"}
	s, _ := getStats(t, env, "/api/keys/"+k.ID+"/usage")
	if s.Summary["requests"] != 8.0 || s.Summary["errors"] != 2.0 || len(s.Groups) != 0 {
		t.Errorf("usage = %+v", s)
	}
	if code, body := env.do(t, env.client, http.MethodGet, "/api/keys/nope/usage", ""); code != http.StatusNotFound ||
		errCode(t, body) != "not_found" {
		t.Errorf("unknown key = %d %s", code, body)
	}
}

// The front door logs every subscription provider under one provider, so each is told apart by its models.
func TestStatsSubscriptionProvidersSplitByModel(t *testing.T) {
	ts := time.Now().UTC()
	env := newStatsEnv(t,
		bifrost.Log{Timestamp: ts, Status: "success", Provider: bifrost.OAuthProvider, Model: "antigravity/g-1"},
		bifrost.Log{Timestamp: ts, Status: "success", Provider: bifrost.OAuthProvider, Model: "antigravity/g-2(8192)"},
		bifrost.Log{Timestamp: ts, Status: "success", Provider: bifrost.OAuthProvider, Model: "codex/c-1"},
		bifrost.Log{Timestamp: ts, Status: "success", Provider: "or", Model: "antigravity/g-1"},
	)
	env.server.d.Names = namesAdapter{}
	env.server.d.Accounts = nil // Historical stats do not depend on account state.
	s, raw := getStats(t, env, "/api/stats?group_by=provider")
	got := map[string]float64{}
	for _, g := range s.Groups {
		got[g.Name] = g.Summary["requests"].(float64)
	}
	if len(got) != 2 || got["antigravity"] != 2 || got["codex"] != 1 {
		t.Fatalf("groups = %s", raw)
	}
	s, raw = getStats(t, env, "/api/stats?group_by=provider&provider=antigravity")
	if len(s.Groups) != 1 || s.Groups[0].Name != "antigravity" || s.Summary["requests"] != 2.0 {
		t.Fatalf("provider-scoped stats = %s", raw)
	}

	env.logs.models = []string{"antigravity/g-1", "antigravity/g-2(8192)", "codex/c-1", "or/antigravity/x"}
	if code, body := env.do(t, env.client, http.MethodGet, "/api/logs?provider=antigravity", ""); code != http.StatusOK {
		t.Fatalf("logs = %d %s", code, body)
	}
	if f, _ := env.logs.lastSearch(); !slices.Equal(f.Models, []string{"antigravity/g-1", "antigravity/g-2(8192)"}) {
		t.Fatalf("logs filter models = %v", f.Models)
	}
	code, body := env.do(t, env.client, http.MethodGet, "/api/logs?provider=grok", "")
	if code != http.StatusOK || strings.TrimSpace(string(body)) != `{"items":[],"total":0}` {
		t.Fatalf("a subscription provider that served nothing = %d %s", code, body)
	}
}

func TestStatsSeries(t *testing.T) {
	t0 := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
	t1, t2 := t0.Add(time.Hour), t0.Add(2*time.Hour)
	at := t1.Add(5 * time.Minute)
	env := newStatsEnv(t,
		bifrost.Log{Timestamp: at, Status: "success", Latency: new(80.0)},
		bifrost.Log{Timestamp: at, Status: "success", Latency: new(40.0)},
		bifrost.Log{Timestamp: at, Status: "success", Latency: new(60.0), Cost: new(0.25), TokenUsage: &bifrost.TokenUsage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}},
		bifrost.Log{Timestamp: at, Status: "error"},
		bifrost.Log{Timestamp: t0.Add(-time.Hour), Status: "success", Latency: new(1.0)},
		bifrost.Log{Timestamp: t2, Status: "success", Latency: new(500.0)},
	)
	env.server.now = func() time.Time { return t2 }

	s, raw := getStats(t, env, "/api/stats?from="+t0.Format(time.RFC3339))
	if len(s.Series) != 13 {
		t.Fatalf("want 13 ten-minute buckets (including the end): %s", raw)
	}
	want := map[string]any{
		"requests": 4.0, "errors": 1.0, "prompt_tokens": 10.0, "completion_tokens": 5.0, "total_tokens": 15.0,
		"cost_usd": 0.25, "time": t1.Format(time.RFC3339),
	}
	for k, v := range want {
		if s.Series[6][k] != v {
			t.Errorf("series[6].%s = %v, want %v", k, s.Series[6][k], v)
		}
	}
	checkStatsLatency(t, s.Series[6]["latency_p50_ms"], 60)
	checkStatsLatency(t, s.Series[6]["latency_p95_ms"], 80)
	for _, k := range []string{"requests", "errors", "prompt_tokens", "completion_tokens", "total_tokens", "cost_usd"} {
		if v, ok := s.Series[0][k]; !ok || v != 0.0 {
			t.Errorf("series[0].%s = %v (present %v), want 0", k, v, ok)
		}
	}
	if s.Series[0]["latency_p50_ms"] != nil || s.Series[0]["latency_p95_ms"] != nil {
		t.Errorf("empty bucket percentiles = %s", raw)
	}
	checkStatsLatency(t, s.Series[12]["latency_p95_ms"], 500)
}

func TestStatsGroupByModel(t *testing.T) {
	ts := time.Now().UTC()
	row := bifrost.Log{Timestamp: ts, Status: "success", Provider: "fd-or", Model: "gpt-x"}
	env := newStatsEnv(t, row, row, row, row,
		bifrost.Log{Timestamp: ts, Status: "success", Provider: bifrost.OAuthProvider, Model: "codex/c-1"},
		bifrost.Log{Timestamp: ts, Status: "error", Provider: bifrost.OAuthProvider, Model: "codex/c-1"},
	)
	env.names.targets = map[[2]string]alias.Target{{"fd-or", "gpt-x"}: {Provider: "or", Model: "gpt-x"}}
	s, raw := getStats(t, env, "/api/stats?group_by=model")
	if len(s.Groups) != 2 || s.Groups[0].Name != "or/gpt-x" || s.Groups[1].Name != "codex/c-1" ||
		s.Groups[0].Summary["requests"] != 4.0 || s.Groups[1].Summary["errors"] != 1.0 {
		t.Fatalf("groups = %s", raw)
	}
	s, raw = getStats(t, env, "/api/stats?group_by=model&provider=or")
	if len(s.Groups) != 1 || s.Groups[0].Name != "or/gpt-x" || s.Groups[0].Summary["requests"] != 4.0 {
		t.Errorf("provider-scoped model groups = %s", raw)
	}
}
