package quota

import (
	"testing"
	"time"

	"github.com/47vigen/remora/internal/store"
)

func TestPassiveCodex(t *testing.T) {
	observed := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	q, ok := passive("codex", map[string]string{
		"X-Codex-Plan-Type":                "plus",
		"x-codex-primary-used-percent":     "25",
		"X-Codex-Primary-Window-Minutes":   "300",
		"x-codex-primary-reset-at":         "1792152000",
		"x-codex-secondary-used-percent":   "50",
		"x-codex-secondary-window-minutes": "10080",
		"x-codex-limit-reached":            "true",
	}, observed)
	if !ok {
		t.Fatal("ok = false")
	}
	reset := time.Unix(1792152000, 0).UTC()
	checkWindows(t, q.Windows, []store.QuotaWindow{win("", "5h", f(0.75), &reset), win("", "weekly", f(0.5), nil)})
	if q.Plan != "plus" || !q.LimitReached || q.Source != "passive" || !q.FetchedAt.Equal(observed) {
		t.Errorf("passive = %+v", q)
	}
}

func TestPassiveCodexResetFromObservedAt(t *testing.T) {
	observed := time.Now().Add(-time.Hour).UTC()
	q, ok := passive("codex", map[string]string{
		"x-codex-primary-used-percent":        "10",
		"x-codex-primary-window-minutes":      "300",
		"x-codex-primary-reset-after-seconds": "3600",
	}, observed)
	if !ok || len(q.Windows) != 1 || q.Windows[0].ResetsAt == nil || !q.Windows[0].ResetsAt.Equal(observed.Add(time.Hour)) {
		t.Errorf("passive = %+v, %v; want reset at observedAt+1h", q, ok)
	}
}

func TestPassiveClaude(t *testing.T) {
	observed := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	q, ok := passive("claude", map[string]string{
		"anthropic-ratelimit-unified-5h-utilization": "0.42",
		"anthropic-ratelimit-unified-5h-reset":       "1792152000",
		"Anthropic-Ratelimit-Unified-7d-Utilization": "1",
	}, observed)
	reset := time.Unix(1792152000, 0).UTC()
	if !ok {
		t.Fatal("ok = false")
	}
	checkWindows(t, q.Windows, []store.QuotaWindow{win("", "5h", f(0.58), &reset), win("", "weekly", f(0), nil)})
	if !q.LimitReached {
		t.Error("LimitReached = false with 7d at 0")
	}

	q, ok = passive("claude", map[string]string{
		"anthropic-ratelimit-unified-5h-utilization": "0.1",
		"anthropic-ratelimit-unified-status":         "rejected",
	}, observed)
	if !ok || !q.LimitReached {
		t.Errorf("rejected: %+v, %v", q, ok)
	}
	if _, ok := passive("claude", map[string]string{"retry-after": "30"}, observed); ok {
		t.Error("retry-after alone: ok")
	}
	if _, ok := passive("antigravity", map[string]string{"x-codex-primary-used-percent": "1"}, observed); ok {
		t.Error("antigravity: ok")
	}
}
