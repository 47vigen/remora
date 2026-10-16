package quota

import (
	"testing"
	"time"

	"github.com/47vigen/remora/internal/engine/cliproxyapi"
	"github.com/47vigen/remora/internal/store"
)

func TestNextPoll(t *testing.T) {
	const iv = 15 * time.Minute
	n := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	ag := store.Account{ID: 1, Provider: "antigravity", Enabled: true}
	cred := &cliproxyapi.Credential{UpdatedAt: n.Add(-48 * time.Hour)}
	checked := func(ago time.Duration) *store.AccountQuota {
		return &store.AccountQuota{CheckedAt: n.Add(-ago)}
	}

	off := []struct {
		name string
		a    store.Account
		c    *cliproxyapi.Credential
		iv   time.Duration
	}{
		{"disabled row", store.Account{Provider: "antigravity"}, cred, iv},
		{"disabled credential", ag, &cliproxyapi.Credential{Disabled: true}, iv},
		{"no credential", ag, nil, iv},
		{"unsupported", store.Account{Provider: "kimi", Enabled: true}, cred, iv},
		{"interval 0", ag, cred, 0},
	}
	for _, tt := range off {
		if _, ok := nextPoll(tt.a, tt.c, nil, 0, tt.iv); ok {
			t.Errorf("%s: ok", tt.name)
		}
	}

	want := func(name string, a store.Account, c *cliproxyapi.Credential, q *store.AccountQuota, failures int, at time.Time) {
		t.Helper()
		got, ok := nextPoll(a, c, q, failures, iv)
		if !ok || !got.Equal(at) {
			t.Errorf("%s: nextPoll = %v, %v; want %v", name, got, ok, at)
		}
	}
	want("never checked", ag, cred, nil, 0, time.Time{})
	want("20m ago", ag, cred, checked(20*time.Minute), 0, n.Add(-5*time.Minute))
	want("1m ago", ag, cred, checked(time.Minute), 0, n.Add(14*time.Minute))
	for failures, gap := range map[int]time.Duration{1: 30 * time.Minute, 2: time.Hour, 5: 4 * time.Hour, 9: 4 * time.Hour, 70: 4 * time.Hour} {
		want("backoff", ag, cred, checked(0), failures, n.Add(gap))
	}

	observed := n.Add(-2 * time.Minute)
	codex := store.Account{ID: 2, Provider: "codex", Enabled: true}
	passiveCred := &cliproxyapi.Credential{Quota: cliproxyapi.Quota{ObservedAt: &observed}}
	want("fresh passive", codex, passiveCred, checked(30*time.Minute), 0, n.Add(13*time.Minute))
	want("fresh passive, never polled", codex, passiveCred, nil, 0, n.Add(13*time.Minute))
	want("passive ignored for antigravity", ag, passiveCred, checked(30*time.Minute), 0, n.Add(-15*time.Minute))

	r := n.Add(2 * time.Hour)
	exhausted := &store.AccountQuota{
		CheckedAt: n.Add(-time.Minute), LimitReached: true,
		Windows: []store.QuotaWindow{win("gemini", "5h", f(0), &r), win("gemini", "weekly", f(0.5), new(n.Add(time.Hour)))},
	}
	want("exhausted", ag, cred, exhausted, 0, r)
	after := *exhausted
	after.CheckedAt = r
	want("after the reset", ag, cred, &after, 0, r.Add(iv))

	signIn := &store.AccountQuota{CheckedAt: n.Add(-time.Hour), Error: MsgSignIn}
	if _, ok := nextPoll(ag, cred, signIn, 0, iv); ok {
		t.Error("sign in again, credential unchanged: ok")
	}
	want("sign in again, credential changed", ag, &cliproxyapi.Credential{UpdatedAt: n}, signIn, 0, n.Add(-45*time.Minute))
}
