package proxy

import (
	"math/rand/v2"
	"sync/atomic"
	"time"

	"github.com/47vigen/remora/internal/alias"
)

type pickChain struct {
	alias.Chain
	cursor *atomic.Uint64 // round-robin; kept across SetChains for the same alias
}

// Picker picks the member a spreading alias's request tries first, from the live chains the reconciler publishes
// and the starts of the last minute. It is safe for concurrent use.
type Picker struct {
	chains atomic.Pointer[map[string]pickChain]
	recent *recentStarts
	intn   func(n int) int
}

// NewPicker returns a Picker; nil now and intn mean time.Now and rand.IntN.
func NewPicker(now func() time.Time, intn func(n int) int) *Picker {
	if intn == nil {
		intn = rand.IntN
	}
	return &Picker{recent: newRecentStarts(now), intn: intn}
}

// SetChains replaces every published chain; the reconciler calls it after each committed pass.
func (p *Picker) SetChains(chains map[string]alias.Chain) {
	old := p.chains.Load()
	next := make(map[string]pickChain, len(chains))
	for name, c := range chains {
		pc := pickChain{Chain: c, cursor: new(atomic.Uint64)}
		if old != nil {
			if prev, ok := (*old)[name]; ok {
				pc.cursor = prev.cursor
			}
		}
		next[name] = pc
	}
	p.chains.Store(&next)
}

// Pick returns the member ref to send as X-Remora-First for aliasName, or "" when it has no published chain.
// Ties go to the lower index (declared order).
func (p *Picker) Pick(aliasName string) string {
	m := p.chains.Load()
	if m == nil {
		return ""
	}
	c, ok := (*m)[aliasName]
	if !ok || len(c.Members) == 0 {
		return ""
	}
	n := len(c.Members)
	switch c.Strategy {
	case alias.StrategyRoundRobin:
		return c.Members[(c.cursor.Add(1)-1)%uint64(n)]
	case alias.StrategyRandom:
		return c.Members[p.intn(n)]
	case alias.StrategyP2C:
		if n == 1 {
			return c.Members[0]
		}
		i, j := p.intn(n), p.intn(n)
		for j == i {
			j = p.intn(n)
		}
		i, j = min(i, j), max(i, j)
		if p.recent.Count(c.Members[j]) < p.recent.Count(c.Members[i]) {
			return c.Members[j]
		}
		return c.Members[i]
	case alias.StrategyLeastUsed:
		best, bestN := 0, p.recent.Count(c.Members[0])
		for i := 1; i < n; i++ {
			if k := p.recent.Count(c.Members[i]); k < bestN {
				best, bestN = i, k
			}
		}
		return c.Members[best]
	}
	return ""
}

// Started records a start against the member that served the request (x-remora-served-by), so a fallback counts
// where it landed. "" is ignored.
func (p *Picker) Started(servedBy string) {
	if servedBy != "" {
		p.recent.Add(servedBy)
	}
}

func (p *Picker) starts(ref string) int { return p.recent.Count(ref) }
