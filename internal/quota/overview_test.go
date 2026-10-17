package quota

import (
	"testing"
	"time"

	"github.com/47vigen/remora/internal/accounts"
	"github.com/47vigen/remora/internal/engine/cliproxyapi"
	"github.com/47vigen/remora/internal/store"
)

func acct(id int64, provider string, enabled bool) accounts.Account {
	return accounts.Account{Account: store.Account{ID: id, Provider: provider, Enabled: enabled, Label: provider}}
}

func TestOverviewCountsEachAccountOnce(t *testing.T) {
	n := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	later := n.Add(time.Hour)
	healthy := acct(1, "antigravity", true)
	exhaustedCooling := acct(2, "antigravity", true)
	exhaustedCooling.NextRetryAfter = &later
	cooling := acct(3, "antigravity", true)
	cooling.Cooldowns = []cliproxyapi.Cooldown{{Scope: "model", RetryAt: later}}
	out := acct(4, "antigravity", true)
	signIn := acct(5, "antigravity", true)
	disabled := acct(6, "antigravity", false)
	kimi := acct(7, "kimi", true)
	suspended := acct(8, "antigravity", true)
	pool := []accounts.PoolStatus{{Provider: "antigravity", Enabled: 5, Out: []accounts.OutAccount{{ID: 4}}}}
	snaps := map[int64]store.AccountQuota{
		1: {AccountID: 1}, 2: {AccountID: 2, LimitReached: true}, 5: {AccountID: 5, Error: MsgSignIn}, 7: {AccountID: 7},
		8: {AccountID: 8, Error: MsgSuspended + ": appeal"},
	}
	got := Overview([]accounts.Account{kimi, healthy, exhaustedCooling, cooling, out, signIn, disabled, suspended}, pool, snaps, n)
	if len(got) != 2 || got[0].Provider != "antigravity" || got[1].Provider != "kimi" {
		t.Fatalf("providers = %+v", got)
	}
	ag := got[0]
	if !ag.Supported || ag.Available != 1 || ag.Exhausted != 1 || ag.Cooling != 1 || ag.SignIn != 2 || ag.Suspended != 1 || len(ag.Accounts) != 7 {
		t.Errorf("antigravity = %+v", ag)
	}
	if ag.Accounts[0].Quota == nil || ag.Accounts[2].Quota != nil {
		t.Errorf("antigravity quotas: %+v / %+v", ag.Accounts[0].Quota, ag.Accounts[2].Quota)
	}
	k := got[1]
	if k.Supported || k.Available != 1 || k.Accounts[0].Quota != nil {
		t.Errorf("kimi = %+v", k)
	}
}

func TestOverviewNextReset(t *testing.T) {
	n := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	a, b := acct(1, "codex", true), acct(2, "codex", true)
	reset := func(d time.Duration) *time.Time { return new(n.Add(d)) }
	snaps := map[int64]store.AccountQuota{
		1: {Windows: []store.QuotaWindow{win("", "5h", f(0), reset(2*time.Hour)), win("", "weekly", f(0.5), reset(10*time.Minute))}},
		2: {Windows: []store.QuotaWindow{win("", "5h", f(0), reset(time.Hour))}},
	}
	check := func(name string, snaps map[int64]store.AccountQuota, want *time.Time) {
		t.Helper()
		got := Overview([]accounts.Account{a, b}, nil, snaps, n)[0].NextReset
		if (got == nil) != (want == nil) || (got != nil && !got.Equal(*want)) {
			t.Errorf("%s: NextReset = %v, want %v", name, got, want)
		}
	}
	check("zero windows", snaps, reset(time.Hour))
	check("none at zero", map[int64]store.AccountQuota{1: {Windows: []store.QuotaWindow{win("", "weekly", f(0.5), reset(10*time.Minute))}}}, reset(10*time.Minute))
	check("only past", map[int64]store.AccountQuota{1: {Windows: []store.QuotaWindow{win("", "5h", f(0), reset(-time.Hour))}}}, nil)
}
