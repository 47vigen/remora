package bifrost

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestSearchLogsQuery(t *testing.T) {
	start := time.Date(2026, 10, 14, 9, 30, 0, 123456789, time.UTC)
	var got url.Values
	var path string
	srv := httptest.NewServer(withLogin(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		got = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"logs":[{"id":"a","parent_request_id":null,"timestamp":"2026-10-14T09:30:01Z",` +
			`"provider":"openrouter","model":"m","status":"error"}],"pagination":{"limit":1000,"offset":0,` +
			`"sort_by":"timestamp","order":"asc","total_count":7},"stats":{},"has_logs":true}`))
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL, "admin", "secret", srv.Client())

	page, err := c.SearchLogs(context.Background(), LogFilter{
		Status: []string{"error"}, Start: &start, Limit: 1000, SortBy: "timestamp", Order: "asc",
	})
	if err != nil {
		t.Fatal(err)
	}
	if path != "/api/logs" {
		t.Fatalf("path = %q", path)
	}
	want := map[string]string{
		"status": "error", "start_time": start.Format(time.RFC3339Nano), "sort_by": "timestamp", "order": "asc",
		"limit": "1000",
	}
	for k, v := range want {
		if got.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, got.Get(k), v)
		}
	}
	if got.Has("offset") {
		t.Errorf("offset sent for zero value: %q", got.Get("offset"))
	}
	if page.Total != 7 || len(page.Logs) != 1 {
		t.Fatalf("page = %+v", page)
	}
	l := page.Logs[0]
	if l.ID != "a" || l.Provider != "openrouter" || l.Model != "m" || l.Status != "error" || l.ParentRequestID != nil ||
		!l.Timestamp.Equal(time.Date(2026, 10, 14, 9, 30, 1, 0, time.UTC)) {
		t.Fatalf("log = %+v", l)
	}

	if _, err := c.SearchLogs(context.Background(), LogFilter{Status: []string{"error", "success"}, Offset: 1000}); err != nil {
		t.Fatal(err)
	}
	if got.Get("status") != "error,success" || got.Get("offset") != "1000" || got.Has("start_time") || got.Has("limit") {
		t.Fatalf("query = %v", got)
	}
}

func TestGetLogFailure(t *testing.T) {
	_, c := newFakeEngine(t, map[string]answer{
		"GET /api/logs/r1": {http.StatusOK, `{"id":"r1","timestamp":"2026-10-14T09:30:01Z","provider":"p","model":"m",` +
			`"status":"error","error_details":{"is_bifrost_error":false,"status_code":429,` +
			`"error":{"message":"slow down"},"extra_fields":{"provider":"p","retry_after_ms":3000}}}`},
		"GET /api/logs/r2":   {http.StatusOK, `{"id":"r2","timestamp":"2026-10-14T09:30:01Z","status":"success"}`},
		"GET /api/logs/r3":   {http.StatusOK, `{"id":"r3","status":"error","error_details":{"status_code":402,"extra_fields":{}}}`},
		"GET /api/logs/gone": {http.StatusNotFound, `{"is_bifrost_error":false,"error":{"message":"log not found"}}`},
	})
	ctx := context.Background()

	l, err := c.GetLog(ctx, "r1")
	if err != nil {
		t.Fatal(err)
	}
	if code, ra := l.Failure(); code != 429 || ra != 3*time.Second {
		t.Fatalf("Failure() = (%d, %v), want (429, 3s)", code, ra)
	}

	l, err = c.GetLog(ctx, "r2")
	if err != nil {
		t.Fatal(err)
	}
	if code, ra := l.Failure(); code != 0 || ra != 0 {
		t.Fatalf("Failure() without details = (%d, %v)", code, ra)
	}

	l, err = c.GetLog(ctx, "r3")
	if err != nil {
		t.Fatal(err)
	}
	if code, ra := l.Failure(); code != 402 || ra != 0 {
		t.Fatalf("Failure() = (%d, %v), want (402, 0)", code, ra)
	}

	if _, err := c.GetLog(ctx, "gone"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetLog(gone) = %v, want ErrNotFound", err)
	}
	if _, err := c.GetLog(ctx, ""); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("GetLog(\"\") = %v, want a plain error", err)
	}
}

func TestSearchLogsQueryAllFilters(t *testing.T) {
	t0 := time.Date(2026, 10, 14, 9, 30, 0, 123456789, time.UTC)
	t1 := t0.Add(time.Hour)
	const search = "سلام\u200cدنیا"
	var got url.Values
	srv := httptest.NewServer(withLogin(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"logs":[{"id":"c1","parent_request_id":"r","timestamp":"2026-10-14T09:30:01Z",` +
			`"provider":"p","model":"m","status":"success","fallback_index":2,"virtual_key_id":"vk1",` +
			`"routing_rule_id":"r1","latency":123.5,"cost":0.25,"stream":true,` +
			`"token_usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}}],` +
			`"pagination":{"total_count":41}}`))
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL, "admin", "secret", srv.Client())

	page, err := c.SearchLogs(context.Background(), LogFilter{
		VirtualKeyIDs: []string{"vk1", "vk2"}, RoutingRuleIDs: []string{"r1"}, Providers: []string{"p1", "p2"},
		Models: []string{"m1"}, Status: []string{"error"}, Start: &t0, End: &t1, ContentSearch: search,
		ParentRequestID: "root", Limit: 20, Offset: 40, SortBy: "latency", Order: "asc",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"virtual_key_ids": "vk1,vk2", "routing_rule_ids": "r1", "providers": "p1,p2", "models": "m1",
		"status": "error", "start_time": t0.Format(time.RFC3339Nano), "end_time": t1.Format(time.RFC3339Nano),
		"content_search": search, "parent_request_id": "root", "limit": "20", "offset": "40", "sort_by": "latency",
		"order": "asc",
	}
	for k, v := range want {
		if got.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, got.Get(k), v)
		}
	}
	if got.Get("content_search") != search {
		t.Errorf("content_search bytes = % x, want % x", got.Get("content_search"), search)
	}
	if page.Total != 41 || len(page.Logs) != 1 {
		t.Fatalf("page = %+v", page)
	}
	l := page.Logs[0]
	if l.FallbackIndex != 2 || l.VirtualKeyID == nil || *l.VirtualKeyID != "vk1" || l.RoutingRuleID == nil ||
		*l.RoutingRuleID != "r1" || l.Latency == nil || *l.Latency != 123.5 || l.Cost == nil || *l.Cost != 0.25 ||
		!l.Stream || l.TokenUsage == nil || *l.TokenUsage != (TokenUsage{PromptTokens: 3, CompletionTokens: 4, TotalTokens: 7}) {
		t.Fatalf("log = %+v", l)
	}

	// Zero filters stay out of the query.
	if _, err := c.SearchLogs(context.Background(), LogFilter{}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("empty filter query = %v", got)
	}
}

func TestGetLogBodies(t *testing.T) {
	_, c := newFakeEngine(t, map[string]answer{
		"GET /api/logs/r1": {http.StatusOK, `{"id":"r1","status":"success","input_history":[{"role":"user","content":"hi"}],` +
			`"output_message":{"role":"assistant","content":"yo"},"params":{"temperature":0.5}}`},
	})
	l, err := c.GetLog(context.Background(), "r1")
	if err != nil {
		t.Fatal(err)
	}
	if string(l.InputHistory) != `[{"role":"user","content":"hi"}]` || string(l.OutputMessage) != `{"role":"assistant","content":"yo"}` ||
		string(l.Params) != `{"temperature":0.5}` {
		t.Fatalf("bodies = %s %s %s", l.InputHistory, l.OutputMessage, l.Params)
	}
}

func TestLogStatsDecode(t *testing.T) {
	t0 := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	var got url.Values
	var path string
	srv := httptest.NewServer(withLogin(func(w http.ResponseWriter, r *http.Request) {
		path, got = r.URL.Path, r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"total_requests":210,"success_rate":95,"user_facing_success_rate":97.5,` +
			`"user_facing_total_requests":200,"average_latency":12.5,"total_tokens":900,"prompt_tokens":600,` +
			`"completion_tokens":300,"total_cost":1.75}`))
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL, "admin", "secret", srv.Client())

	st, err := c.LogStats(context.Background(), LogFilter{RoutingRuleIDs: []string{"r1"}, Start: &t0})
	if err != nil {
		t.Fatal(err)
	}
	if path != "/api/logs/stats" || got.Get("routing_rule_ids") != "r1" || got.Get("start_time") != t0.Format(time.RFC3339Nano) {
		t.Fatalf("request = %s %v", path, got)
	}
	want := LogStats{
		UserFacingTotalRequests: 200, UserFacingSuccessRate: 97.5, PromptTokens: 600, CompletionTokens: 300,
		TotalTokens: 900, TotalCost: 1.75,
	}
	if st != want {
		t.Fatalf("stats = %+v, want %+v", st, want)
	}
}

func TestHistogramDecode(t *testing.T) {
	t0 := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	var got url.Values
	var path string
	srv := httptest.NewServer(withLogin(func(w http.ResponseWriter, r *http.Request) {
		path, got = r.URL.Path, r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"buckets":[{"timestamp":"2026-10-14T00:00:00Z","count":5,"success":4,"error":1,"cancelled":0},` +
			`{"timestamp":"2026-10-14T01:00:00Z","count":2,"success":2,"error":0,"cancelled":0}],"bucket_size_seconds":3600}`))
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL, "admin", "secret", srv.Client())

	b, err := c.Histogram(context.Background(), LogFilter{VirtualKeyIDs: []string{"vk1"}, Start: &t0})
	if err != nil {
		t.Fatal(err)
	}
	if path != "/api/logs/histogram" || got.Get("virtual_key_ids") != "vk1" {
		t.Fatalf("request = %s %v", path, got)
	}
	want := []HistogramBucket{
		{Timestamp: t0, Count: 5, Success: 4, Error: 1},
		{Timestamp: t0.Add(time.Hour), Count: 2, Success: 2},
	}
	if len(b) != len(want) {
		t.Fatalf("buckets = %+v", b)
	}
	for i := range want {
		if !b[i].Timestamp.Equal(want[i].Timestamp) || b[i].Count != want[i].Count || b[i].Success != want[i].Success ||
			b[i].Error != want[i].Error {
			t.Errorf("bucket %d = %+v, want %+v", i, b[i], want[i])
		}
	}
}

func TestSeriesHistogramsDecode(t *testing.T) {
	t0 := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	paths := map[string]url.Values{}
	srv := httptest.NewServer(withLogin(func(w http.ResponseWriter, r *http.Request) {
		paths[r.URL.Path] = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/logs/histogram/tokens":
			_, _ = w.Write([]byte(`{"buckets":[{"timestamp":"2026-10-14T00:00:00Z","prompt_tokens":10,"completion_tokens":5,` +
				`"total_tokens":15,"cached_read_tokens":0}],"bucket_size_seconds":3600}`))
		case "/api/logs/histogram/cost":
			_, _ = w.Write([]byte(`{"buckets":[{"timestamp":"2026-10-14T00:00:00Z","total_cost":0.25,"by_model":{}}],` +
				`"bucket_size_seconds":3600,"models":[]}`))
		case "/api/logs/histogram/latency":
			_, _ = w.Write([]byte(`{"buckets":[{"timestamp":"2026-10-14T00:00:00Z","avg_latency":80,"p90_latency":110,` +
				`"p95_latency":120,"p99_latency":150,"total_requests":3}],"bucket_size_seconds":3600}`))
		case "/api/logs/rankings":
			_, _ = w.Write([]byte(`{"rankings":[{"model":"gpt-x","provider":"fd-or","total_requests":4,"trend":{}}]}`))
		}
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL, "admin", "secret", srv.Client())
	ctx := context.Background()
	f := LogFilter{VirtualKeyIDs: []string{"vk1"}, Start: &t0}

	tok, err := c.TokenHistogram(ctx, f)
	if err != nil || len(tok) != 1 || !tok[0].Timestamp.Equal(t0) || tok[0].PromptTokens != 10 ||
		tok[0].CompletionTokens != 5 || tok[0].TotalTokens != 15 {
		t.Fatalf("tokens = %+v, %v", tok, err)
	}
	cost, err := c.CostHistogram(ctx, f)
	if err != nil || len(cost) != 1 || cost[0].TotalCost != 0.25 {
		t.Fatalf("cost = %+v, %v", cost, err)
	}
	lat, err := c.LatencyHistogram(ctx, f)
	if err != nil || len(lat) != 1 || lat[0].P95Latency != 120 || lat[0].TotalRequests != 3 {
		t.Fatalf("latency = %+v, %v", lat, err)
	}
	usage, err := c.ModelUsage(ctx, f)
	if err != nil || len(usage) != 1 || usage[0] != (ModelUsage{Provider: "fd-or", Model: "gpt-x", TotalRequests: 4}) {
		t.Fatalf("model usage = %+v, %v", usage, err)
	}
	for _, p := range []string{"/api/logs/histogram/tokens", "/api/logs/histogram/cost", "/api/logs/histogram/latency", "/api/logs/rankings"} {
		if q, ok := paths[p]; !ok || q.Get("virtual_key_ids") != "vk1" || q.Get("start_time") == "" {
			t.Errorf("%s query = %v (called %v)", p, q, ok)
		}
	}
}
