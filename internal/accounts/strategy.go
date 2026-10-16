package accounts

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"github.com/47vigen/remora/internal/alias"
	"github.com/47vigen/remora/internal/engine/cliproxyapi"
	"github.com/47vigen/remora/internal/store"
)

// NativePriorities maps each credential of one provider's rows to the engine priority tier the strategy needs. The
// engine serves the highest available tier and rotates inside it: priority keeps the rows' numbers; fill-first gives
// each account its own tier, n-1..0 by priority (high first), then oldest, then lowest id, so the order is total
// and stable; the other four put every account in one tier, since they spread over the whole pool.
func NativePriorities(st alias.Strategy, rows []store.Account) map[string]int {
	out := make(map[string]int, len(rows))
	switch st {
	case alias.StrategyPriority:
		for _, r := range rows {
			out[r.Credential] = r.Priority
		}
	case alias.StrategyFillFirst:
		sorted := slices.SortedFunc(slices.Values(rows), func(a, b store.Account) int {
			return cmp.Or(cmp.Compare(b.Priority, a.Priority), a.CreatedAt.Compare(b.CreatedAt), cmp.Compare(a.ID, b.ID))
		})
		for i, r := range sorted {
			out[r.Credential] = len(sorted) - 1 - i
		}
	default:
		for _, r := range rows {
			out[r.Credential] = 0
		}
	}
	return out
}

// EngineStrategies maps the stored strategies to the engine's per-provider strategies, keyed by the engine's
// provider id, every subscription provider listed. random, least-used and p2c need the engine's own selectors; the
// other three are round-robin over the tiers NativePriorities sets.
func EngineStrategies(stored map[string]alias.Strategy) map[string]string {
	out := map[string]string{}
	for _, p := range cliproxyapi.Providers() {
		switch st := stored[p.Name]; st {
		case alias.StrategyRandom, alias.StrategyLeastUsed, alias.StrategyP2C:
			out[p.Engine] = string(st)
		default:
			out[p.Engine] = string(alias.StrategyRoundRobin)
		}
	}
	return out
}

// Strategies returns every subscription provider's strategy, round-robin when none is stored.
func (s *Service) Strategies(ctx context.Context) (map[string]alias.Strategy, error) {
	stored, err := s.store.AccountStrategies(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]alias.Strategy{}
	for _, p := range cliproxyapi.Providers() {
		out[p.Name] = cmp.Or(stored[p.Name], alias.StrategyRoundRobin)
	}
	return out, nil
}

// SetStrategy stores a subscription provider's strategy and applies it to the engine now.
func (s *Service) SetStrategy(ctx context.Context, provider string, st alias.Strategy) error {
	if _, ok := cliproxyapi.ProviderByName(provider); !ok {
		return fmt.Errorf("%w: %s", ErrUnknownProvider, provider)
	}
	if !st.Valid() {
		return fmt.Errorf("accounts: unknown strategy %q", st)
	}
	if err := s.store.SetAccountStrategy(ctx, provider, st); err != nil {
		return storeErr(err, "set strategy of %s", provider)
	}
	return s.Reconcile(ctx)
}

// SetPriority stores an account's priority and applies it to the engine now.
func (s *Service) SetPriority(ctx context.Context, id int64, priority int) error {
	if err := s.store.SetAccountPriority(ctx, id, priority); err != nil {
		return storeErr(err, "set account %d priority", id)
	}
	return s.Reconcile(ctx)
}
