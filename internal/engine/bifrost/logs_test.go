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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
