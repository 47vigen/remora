package quota

import (
	"reflect"
	"testing"
	"time"

	"github.com/47vigen/remora/internal/store"
)

func f(v float64) *float64 { return &v }

func at(s string) *time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return &t
}

func win(scope, window string, remaining *float64, reset *time.Time) store.QuotaWindow {
	return store.QuotaWindow{Scope: scope, Window: window, Remaining: remaining, ResetsAt: reset}
}

func checkWindows(t *testing.T, got, want []store.QuotaWindow) {
	t.Helper()
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("windows:\n got %s\nwant %s", show(got), show(want))
	}
}

func show(ws []store.QuotaWindow) string {
	s := ""
	for _, w := range ws {
		r, rs := "nil", "nil"
		if w.Remaining != nil {
			r = time.Duration(*w.Remaining * 1000).String()
		}
		if w.ResetsAt != nil {
			rs = w.ResetsAt.Format(time.RFC3339)
		}
		s += "(" + w.Scope + "," + w.Window + "," + r + "," + rs + ") "
	}
	return s
}

const agSummaryBody = `{"groups":[
  {"groupId":"gemini","displayName":"Gemini","buckets":[
    {"bucketId":"gemini-session","window":"session","remaining":{"remainingFraction":0.8,"resetTime":"2026-10-16T15:00:00Z"}},
    {"bucketId":"gemini-weekly","window":"weekly","remainingFraction":0.5,"resetTime":"2026-10-20T00:00:00Z"},
    {"bucketId":"gemini-weekly","window":"weekly","remainingFraction":0.1},
    {"bucketId":"gemini-old","window":"5h","disabled":true,"remainingFraction":0.3}
  ]},
  {"id":"claude-gpt","displayName":"Claude & GPT","buckets":[
    {"bucketId":"cg-5h","displayName":"5 hour session","remaining":{"remainingFraction":0,"resetTime":"2026-10-16T13:00:00Z"}},
    {"bucketId":"cg-7d","window":"7d","remaining":{"remainingFraction":0.25,"resetTime":"2026-10-21T00:00:00Z"}}
  ]},
  {"groupId":"other models","displayName":"Other","buckets":[{"window":"5h","remainingFraction":1}]}
]}`

func TestParseAntigravitySummary(t *testing.T) {
	want := []store.QuotaWindow{
		win("gemini", "5h", f(0.8), at("2026-10-16T15:00:00Z")),
		win("gemini", "weekly", f(0.5), at("2026-10-20T00:00:00Z")),
		win("claude-gpt", "5h", f(0), at("2026-10-16T13:00:00Z")),
		win("claude-gpt", "weekly", f(0.25), at("2026-10-21T00:00:00Z")),
		win("other-models", "5h", f(1), nil),
	}
	q, err := parseAntigravitySummary([]byte(agSummaryBody))
	if err != nil {
		t.Fatal(err)
	}
	checkWindows(t, q.Windows, want)
	if !q.LimitReached {
		t.Error("LimitReached = false with a window at 0")
	}
	nested, err := parseAntigravitySummary([]byte(`{"quotaSummary":` + agSummaryBody + `}`))
	if err != nil {
		t.Fatal(err)
	}
	checkWindows(t, nested.Windows, want)
}

func TestParseAntigravityPlan(t *testing.T) {
	for body, want := range map[string]string{
		`{"paidTier":{"id":"g1-pro-tier","name":"Pro"},"currentTier":{"id":"free-tier"}}`: "Pro",
		`{"currentTier":{"id":"free-tier"}}`:                                              "free-tier",
		`{}`:                                                                              "",
		`x`:                                                                               "",
	} {
		if got := parseAntigravityPlan([]byte(body)); got != want {
			t.Errorf("parseAntigravityPlan(%s) = %q, want %q", body, got, want)
		}
	}
}

func TestParseCodexUsage(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	body := `{"plan_type":"plus","rate_limit":{"limit_reached":true,
	  "primary_window":{"used_percent":30,"limit_window_seconds":18000,"reset_at":1792152000},
	  "secondary_window":{"used_percent":60,"limit_window_seconds":604800,"reset_after_seconds":600}},
	  "additional_rate_limits":[{"limit_name":"gpt-5-codex-mini","rate_limit":{
	    "primary_window":{"used_percent":0,"limit_window_seconds":604800,"reset_after_seconds":60}}}]}`
	q, err := parseCodexUsage([]byte(body), now)
	if err != nil {
		t.Fatal(err)
	}
	reset := time.Unix(1792152000, 0).UTC()
	checkWindows(t, q.Windows, []store.QuotaWindow{
		win("", "5h", f(0.7), &reset),
		win("", "weekly", f(0.4), new(now.Add(600*time.Second))),
		win("gpt-5-codex-mini", "weekly", f(1), new(now.Add(time.Minute))),
	})
	if q.Plan != "plus" || !q.LimitReached {
		t.Errorf("plan %q limit %v", q.Plan, q.LimitReached)
	}

	weeklyFirst := `{"rate_limit":{"primary_window":{"used_percent":60,"limit_window_seconds":604800}}}`
	q, _ = parseCodexUsage([]byte(weeklyFirst), now)
	checkWindows(t, q.Windows, []store.QuotaWindow{win("", "weekly", f(0.4), nil)})
	if q.LimitReached {
		t.Error("LimitReached without a signal")
	}
}

func TestParseClaudeUsage(t *testing.T) {
	body := `{"five_hour":{"utilization":10,"resets_at":"2026-10-16T15:00:00Z"},
	  "seven_day":{"utilization":40,"resets_at":"2026-10-20T00:00:00Z"},
	  "seven_day_opus":null,"seven_day_sonnet":{"utilization":100,"resets_at":"2026-10-21T00:00:00Z"}}`
	q, err := parseClaudeUsage([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	checkWindows(t, q.Windows, []store.QuotaWindow{
		win("", "5h", f(0.9), at("2026-10-16T15:00:00Z")),
		win("", "weekly", f(0.6), at("2026-10-20T00:00:00Z")),
		win("sonnet", "weekly", f(0), at("2026-10-21T00:00:00Z")),
	})
	if !q.LimitReached {
		t.Error("LimitReached = false with sonnet at 0")
	}
}

func TestParsersReportMissingAsUnknown(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	q, _ := parseClaudeUsage([]byte(`{"five_hour":{"resets_at":"2026-10-16T15:00:00Z"},"seven_day":{"utilization":"lots"},"seven_day_opus":{"utilization":20}}`))
	checkWindows(t, q.Windows, []store.QuotaWindow{
		win("", "5h", nil, at("2026-10-16T15:00:00Z")),
		win("", "weekly", nil, nil),
		win("opus", "weekly", f(0.8), nil),
	})
	if q.LimitReached {
		t.Error("unknown read as exhausted")
	}
	q, _ = parseCodexUsage([]byte(`{"rate_limit":{"primary_window":{"limit_window_seconds":18000},"secondary_window":{"used_percent":50,"limit_window_seconds":604800}}}`), now)
	checkWindows(t, q.Windows, []store.QuotaWindow{win("", "5h", nil, nil), win("", "weekly", f(0.5), nil)})
	q, _ = parseAntigravitySummary([]byte(`{"groups":[{"groupId":"gemini","buckets":[{"window":"5h"},{"window":"weekly","remainingFraction":0.5}]}]}`))
	checkWindows(t, q.Windows, []store.QuotaWindow{win("gemini", "5h", nil, nil), win("gemini", "weekly", f(0.5), nil)})

	parsers := map[string]func([]byte) (store.AccountQuota, error){
		"antigravity": parseAntigravitySummary,
		"codex":       func(b []byte) (store.AccountQuota, error) { return parseCodexUsage(b, now) },
		"claude":      parseClaudeUsage,
	}
	for name, parse := range parsers {
		for _, bad := range []string{`[]`, `x`} {
			if _, err := parse([]byte(bad)); err == nil {
				t.Errorf("%s(%s): no error", name, bad)
			}
		}
		q, err := parse([]byte(`{}`))
		if err != nil || len(q.Windows) != 0 || q.LimitReached {
			t.Errorf("%s({}) = %+v, %v; want no windows, no error", name, q, err)
		}
	}
}

func TestWindowLabel(t *testing.T) {
	for secs, want := range map[int64]string{18000: "5h", 17000: "5h", 604800: "weekly", 86400: "1440", 0: "0"} {
		if got := windowLabel(secs); got != want {
			t.Errorf("windowLabel(%d) = %q, want %q", secs, got, want)
		}
	}
}
