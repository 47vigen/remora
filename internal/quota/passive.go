package quota

import (
	"strconv"
	"strings"
	"time"

	"github.com/47vigen/remora/internal/store"
)

// passive turns the quota headers the engine recorded from an account's last real response into a snapshot. ok is
// false when nothing in signals names a window.
func passive(provider string, signals map[string]string, observedAt time.Time) (store.AccountQuota, bool) {
	sig := make(map[string]string, len(signals))
	for k, v := range signals {
		sig[strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(v)
	}
	num := func(k string) *float64 {
		v, err := strconv.ParseFloat(sig[k], 64)
		if err != nil {
			return nil
		}
		return &v
	}
	q := store.AccountQuota{Source: "passive", FetchedAt: observedAt}
	switch provider {
	case "codex":
		q.Plan = sig["x-codex-plan-type"]
		q.LimitReached = strings.EqualFold(sig["x-codex-limit-reached"], "true")
		for _, slot := range []string{"x-codex-primary-", "x-codex-secondary-"} {
			used, minutes := num(slot+"used-percent"), num(slot+"window-minutes")
			if used == nil && minutes == nil {
				continue
			}
			var secs int64
			if minutes != nil {
				secs = int64(*minutes * 60)
			}
			var reset *time.Time
			if v := num(slot + "reset-at"); v != nil {
				reset = unixTime(*v)
			} else if v := num(slot + "reset-after-seconds"); v != nil {
				reset = new(observedAt.Add(time.Duration(*v) * time.Second).UTC())
			}
			q.Windows = append(q.Windows, store.QuotaWindow{Window: windowLabel(secs), Remaining: leftOfPercent(used), ResetsAt: reset})
		}
	case "claude":
		const p = "anthropic-ratelimit-unified-"
		q.LimitReached = sig[p+"status"] == "rejected" || sig[p+"7d-status"] == "rejected"
		for _, w := range []struct{ key, window string }{{"5h", "5h"}, {"7d", "weekly"}} {
			// shortcut: utilization assumed a fraction; if recorded headers show percent, divide by 100
			used, resetAt := num(p+w.key+"-utilization"), num(p+w.key+"-reset")
			if used == nil && resetAt == nil {
				continue
			}
			window := store.QuotaWindow{Window: w.window}
			if used != nil {
				window.Remaining = fraction(new(1 - *used))
			}
			if resetAt != nil {
				window.ResetsAt = unixTime(*resetAt)
			}
			q.Windows = append(q.Windows, window)
		}
	default:
		return store.AccountQuota{}, false
	}
	if len(q.Windows) == 0 {
		return store.AccountQuota{}, false
	}
	return finish(q), true
}
