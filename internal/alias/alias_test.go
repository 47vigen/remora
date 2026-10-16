package alias

import "testing"

func TestStrategyNames(t *testing.T) {
	for _, s := range Strategies {
		if !s.Valid() {
			t.Errorf("%q.Valid() = false", s)
		}
	}
	if len(Strategies) != 6 {
		t.Errorf("len(Strategies) = %d, want 6", len(Strategies))
	}
	for _, s := range []Strategy{"", "fallback", "Round-Robin"} {
		if s.Valid() {
			t.Errorf("%q.Valid() = true", s)
		}
	}
	spreads := map[Strategy]bool{StrategyRoundRobin: true, StrategyRandom: true, StrategyP2C: true, StrategyLeastUsed: true}
	for _, s := range Strategies {
		if s.Spreads() != spreads[s] {
			t.Errorf("%q.Spreads() = %v, want %v", s, s.Spreads(), spreads[s])
		}
	}
}
