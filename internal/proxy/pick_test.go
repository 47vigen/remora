package proxy

import (
	"sync"
	"testing"
	"time"

	"github.com/47vigen/remora/internal/alias"
)

// draws returns an intn stub that yields vals in turn.
func draws(vals ...int) func(int) int {
	var mu sync.Mutex
	return func(int) int {
		mu.Lock()
		defer mu.Unlock()
		v := vals[0]
		vals = vals[1:]
		return v
	}
}

func chains(st alias.Strategy, members ...string) map[string]alias.Chain {
	return map[string]alias.Chain{"s": {Strategy: st, Members: members}}
}

func started(p *Picker, ref string, n int) {
	for range n {
		p.Started(ref)
	}
}

func TestPickRoundRobin(t *testing.T) {
	p := NewPicker(nil, nil)
	c := chains(alias.StrategyRoundRobin, "a", "b", "c")
	c["t"] = alias.Chain{Strategy: alias.StrategyRoundRobin, Members: []string{"x", "y"}}
	p.SetChains(c)
	var got []string
	for range 4 {
		got = append(got, p.Pick("s"))
	}
	got = append(got, p.Pick("t"))
	if want := []string{"a", "b", "c", "a", "x"}; !equal(got, want) {
		t.Errorf("picks = %v, want %v", got, want)
	}
	// A republished chain keeps the alias's cursor.
	p.SetChains(c)
	if got := p.Pick("s"); got != "b" {
		t.Errorf("pick after SetChains = %q, want b", got)
	}
}

func TestPickRandom(t *testing.T) {
	p := NewPicker(nil, draws(2))
	p.SetChains(chains(alias.StrategyRandom, "a", "b", "c"))
	if got := p.Pick("s"); got != "c" {
		t.Errorf("pick = %q, want c", got)
	}
	p = NewPicker(nil, nil)
	p.SetChains(chains(alias.StrategyRandom, "a", "b", "c"))
	for range 100 {
		if got := p.Pick("s"); got != "a" && got != "b" && got != "c" {
			t.Fatalf("pick = %q, not a member", got)
		}
	}
}

func TestPickP2C(t *testing.T) {
	setup := func(intn func(int) int, a, b, c int) *Picker {
		p := NewPicker(nil, intn)
		p.SetChains(chains(alias.StrategyP2C, "a", "b", "c"))
		started(p, "a", a)
		started(p, "b", b)
		started(p, "c", c)
		return p
	}
	if got := setup(draws(0, 1), 3, 1, 0).Pick("s"); got != "b" {
		t.Errorf("draws (0,1) = %q, want b", got)
	}
	if got := setup(draws(0, 0, 2), 3, 1, 0).Pick("s"); got != "c" {
		t.Errorf("draws (0,0,2) = %q, want c", got)
	}
	if got := setup(draws(1, 0), 0, 0, 0).Pick("s"); got != "a" {
		t.Errorf("tie drawn (1,0) = %q, want a", got)
	}
}

func TestPickLeastUsed(t *testing.T) {
	p := NewPicker(nil, nil)
	p.SetChains(chains(alias.StrategyLeastUsed, "a", "b", "c"))
	if got := p.Pick("s"); got != "a" {
		t.Errorf("all zero = %q, want a", got)
	}
	started(p, "a", 2)
	if got := p.Pick("s"); got != "b" {
		t.Errorf("a=2 = %q, want b", got)
	}
}

func TestPickWithoutChain(t *testing.T) {
	p := NewPicker(nil, nil)
	if got := p.Pick("s"); got != "" {
		t.Errorf("no chains = %q", got)
	}
	p.SetChains(chains(alias.StrategyRandom, "a", "b"))
	if got := p.Pick("other"); got != "" {
		t.Errorf("unknown alias = %q", got)
	}
}

func TestSetChainsShrinks(t *testing.T) {
	p := NewPicker(nil, nil)
	p.SetChains(chains(alias.StrategyRoundRobin, "a", "b", "c"))
	p.Pick("s")
	p.Pick("s") // the cursor now points at c
	p.SetChains(chains(alias.StrategyRoundRobin, "a", "b"))
	for range 10 {
		if got := p.Pick("s"); got == "c" {
			t.Fatal("picked c after it left the chain")
		}
	}
}

func TestPickConcurrent(t *testing.T) {
	p := NewPicker(nil, nil)
	members := []string{"a", "b", "c", "d"}
	p.SetChains(chains(alias.StrategyP2C, members...))
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 1000 {
				p.Started(p.Pick("s"))
			}
		})
	}
	wg.Go(func() { p.SetChains(chains(alias.StrategyP2C, members...)) })
	wg.Wait()
	sum := 0
	for _, m := range members {
		sum += p.starts(m)
	}
	if sum != 8000 {
		t.Errorf("starts sum = %d, want 8000", sum)
	}
}

func TestPickerUsesClock(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	p := NewPicker(func() time.Time { return now }, nil)
	p.Started("a")
	p.Started("")
	now = now.Add(61 * time.Second)
	if got := p.starts("a"); got != 0 {
		t.Errorf("starts after the window = %d", got)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
