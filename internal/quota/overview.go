package quota

import (
	"slices"
	"strings"
	"time"

	"github.com/47vigen/remora/internal/accounts"
	"github.com/47vigen/remora/internal/engine/cliproxyapi"
	"github.com/47vigen/remora/internal/store"
)

// AccountView is an account with its quota snapshot; Quota is nil when it was never checked or has no fetcher.
type AccountView struct {
	Account accounts.Account
	Quota   *store.AccountQuota
}

// ProviderView is one provider's accounts. Each enabled account is counted once, in the first of Suspended, SignIn,
// Exhausted, Cooling and Available that matches.
type ProviderView struct {
	Provider                              string
	Supported                             bool
	Available, Exhausted, Cooling, SignIn int
	Suspended                             int
	NextReset                             *time.Time
	Accounts                              []AccountView
}

// Overview groups accounts by provider, sorted, listing only providers with accounts. Suspended: the provider
// suspended the account. SignIn: out of the pool, or
// the snapshot asks for a new sign-in. Exhausted: the limit is reached. Cooling: a retry time or cooldown after now.
// NextReset: the earliest future reset among windows at 0, else among all windows.
func Overview(accts []accounts.Account, pool []accounts.PoolStatus, snaps map[int64]store.AccountQuota, now time.Time) []ProviderView {
	out := map[int64]bool{}
	for _, p := range pool {
		for _, o := range p.Out {
			out[o.ID] = true
		}
	}
	byProvider := map[string]*ProviderView{}
	var views []*ProviderView
	zeroReset, anyReset := map[string]*time.Time{}, map[string]*time.Time{}
	earliest := func(m map[string]*time.Time, k string, t *time.Time) {
		if t != nil && t.After(now) && (m[k] == nil || t.Before(*m[k])) {
			m[k] = t
		}
	}
	for _, a := range accts {
		v := byProvider[a.Provider]
		if v == nil {
			v = &ProviderView{Provider: a.Provider, Supported: Supported(a.Provider)}
			byProvider[a.Provider] = v
			views = append(views, v)
		}
		av := AccountView{Account: a}
		if q, ok := snaps[a.ID]; ok && v.Supported {
			av.Quota = &q
		}
		v.Accounts = append(v.Accounts, av)
		if !a.Enabled {
			continue
		}
		switch q := av.Quota; {
		case q != nil && Suspended(q.Error):
			v.Suspended++
		case out[a.ID] || (q != nil && q.Error == MsgSignIn):
			v.SignIn++
		case q != nil && q.LimitReached:
			v.Exhausted++
		case cooling(a, now):
			v.Cooling++
		default:
			v.Available++
		}
		if av.Quota != nil {
			for _, w := range av.Quota.Windows {
				if w.Remaining != nil && *w.Remaining == 0 {
					earliest(zeroReset, a.Provider, w.ResetsAt)
				}
				earliest(anyReset, a.Provider, w.ResetsAt)
			}
		}
	}
	slices.SortFunc(views, func(a, b *ProviderView) int { return strings.Compare(a.Provider, b.Provider) })
	res := make([]ProviderView, 0, len(views))
	for _, v := range views {
		v.NextReset = zeroReset[v.Provider]
		if v.NextReset == nil {
			v.NextReset = anyReset[v.Provider]
		}
		res = append(res, *v)
	}
	return res
}

func cooling(a accounts.Account, now time.Time) bool {
	if a.NextRetryAfter != nil && a.NextRetryAfter.After(now) {
		return true
	}
	return slices.ContainsFunc(a.Cooldowns, func(c cliproxyapi.Cooldown) bool { return c.RetryAt.After(now) })
}
