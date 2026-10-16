package quota

import (
	"time"

	"github.com/47vigen/remora/internal/engine/cliproxyapi"
	"github.com/47vigen/remora/internal/store"
)

// Polling bounds.
const (
	DefaultInterval = 15 * time.Minute
	MinInterval     = 5 * time.Minute
	Spacing         = 2 * time.Second // between any two upstream calls
	RefreshFloor    = 60 * time.Second
	MaxBackoff      = 4 * time.Hour
)

// nextPoll says when a background poll of a is due; ok false means it is not polled in the background. q is its
// stored snapshot (nil = never checked), failures its consecutive 429/5xx count.
func nextPoll(a store.Account, c *cliproxyapi.Credential, q *store.AccountQuota, failures int, interval time.Duration) (time.Time, bool) {
	switch {
	case interval == 0, !a.Enabled, c == nil, c.Disabled, !Supported(a.Provider):
		return time.Time{}, false
	case q != nil && q.Error == MsgSignIn && !c.UpdatedAt.After(q.CheckedAt):
		return time.Time{}, false
	}
	var at time.Time
	if q != nil {
		backoff := MaxBackoff
		if failures < 16 {
			backoff = min(interval<<failures, MaxBackoff)
		}
		at = q.CheckedAt.Add(backoff)
	}
	// Recent traffic already told the engine this account's quota, so a poll would only repeat it.
	if (a.Provider == "codex" || a.Provider == "claude") && c.Quota.ObservedAt != nil {
		if fresh := c.Quota.ObservedAt.Add(interval); fresh.After(at) {
			at = fresh
		}
	}
	if q != nil && exhausted(*q) {
		if r := earliestZeroReset(*q); r != nil && r.After(q.CheckedAt) {
			at = *r
		}
	}
	return at, true
}

// exhausted: every window is known and at 0, or, with no windows, the provider says the limit is reached. One
// empty window must not hold back polling of the others.
func exhausted(q store.AccountQuota) bool {
	if len(q.Windows) == 0 {
		return q.LimitReached
	}
	for _, w := range q.Windows {
		if w.Remaining == nil || *w.Remaining > 0 {
			return false
		}
	}
	return len(q.Windows) > 0
}

func earliestZeroReset(q store.AccountQuota) *time.Time {
	var out *time.Time
	for _, w := range q.Windows {
		if w.Remaining != nil && *w.Remaining == 0 && w.ResetsAt != nil && (out == nil || w.ResetsAt.Before(*out)) {
			out = w.ResetsAt
		}
	}
	return out
}
