package accounts

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/47vigen/remora/internal/alias"
	"github.com/47vigen/remora/internal/engine/cliproxyapi"
	"github.com/47vigen/remora/internal/store"
)

func TestNativePriorities(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rows := []store.Account{
		{ID: 1, Credential: "a", Priority: 5, CreatedAt: t0.Add(time.Hour)},
		{ID: 3, Credential: "c", Priority: 0, CreatedAt: t0},
		{ID: 2, Credential: "b", Priority: 0, CreatedAt: t0},
	}
	zero := map[string]int{"a": 0, "b": 0, "c": 0}
	for _, st := range []alias.Strategy{alias.StrategyRoundRobin, alias.StrategyRandom, alias.StrategyLeastUsed, alias.StrategyP2C} {
		if got := NativePriorities(st, rows); !reflect.DeepEqual(got, zero) {
			t.Errorf("%s = %v, want one tier", st, got)
		}
	}
	if got, want := NativePriorities(alias.StrategyPriority, rows), map[string]int{"a": 5, "b": 0, "c": 0}; !reflect.DeepEqual(got, want) {
		t.Errorf("priority = %v, want %v", got, want)
	}
	// Equal priority and creation time: the lower id goes first, so the order is total and stable.
	if got, want := NativePriorities(alias.StrategyFillFirst, rows), map[string]int{"a": 2, "b": 1, "c": 0}; !reflect.DeepEqual(got, want) {
		t.Errorf("fill-first = %v, want %v", got, want)
	}
}

func TestEngineStrategies(t *testing.T) {
	all := EngineStrategies(nil)
	if len(all) != len(cliproxyapi.Providers()) {
		t.Fatalf("EngineStrategies(nil) = %v", all)
	}
	for _, p := range cliproxyapi.Providers() {
		if all[p.Engine] != "round-robin" {
			t.Errorf("%s = %q, want round-robin", p.Engine, all[p.Engine])
		}
	}
	got := EngineStrategies(map[string]alias.Strategy{"codex": alias.StrategyP2C, "grok": alias.StrategyFillFirst})
	if got["codex"] != "p2c" || got["xai"] != "round-robin" {
		t.Errorf("EngineStrategies = %v", got)
	}
}

func TestReconcileAppliesPriorities(t *testing.T) {
	e := newEnv(t)
	e.setClientKey(t)
	ctx := context.Background()
	e.fake.AddCredential(cliproxyapi.Credential{Name: "codex-a.json", Provider: "codex"})
	e.fake.AddCredential(cliproxyapi.Credential{Name: "codex-b.json", Provider: "codex"})
	e.addRow(t, "codex", "a", "codex-a.json", true)
	e.addRow(t, "codex", "b", "codex-b.json", true)
	if err := e.store.SetAccountStrategy(ctx, "codex", alias.StrategyFillFirst); err != nil {
		t.Fatal(err)
	}
	e.reconcile(t)
	if n := e.fake.Calls("/credentials/fields"); n != 1 {
		t.Errorf("fields calls = %d, want 1 (the older account to tier 1)", n)
	}
	if a, _, _ := e.fake.Credential("codex-a.json"); a.Priority != 1 {
		t.Errorf("codex-a priority = %d, want 1", a.Priority)
	}
	e.reconcile(t)
	if n := e.fake.Calls("/credentials/fields"); n != 1 {
		t.Errorf("an agreeing pass sent %d more field changes", n-1)
	}
}

func TestSetStrategyAppliesNow(t *testing.T) {
	e := newEnv(t)
	e.setClientKey(t)
	ctx := context.Background()
	if err := e.svc.SetStrategy(ctx, "codex", alias.StrategyP2C); err != nil {
		t.Fatalf("SetStrategy: %v", err)
	}
	if got, _ := e.store.AccountStrategies(ctx); got["codex"] != alias.StrategyP2C {
		t.Errorf("stored = %v", got)
	}
	patches := e.fake.ConfigPatches()
	if len(patches) == 0 || at(decodePatch(t, patches[len(patches)-1]), "routing.provider-strategies.codex") != "p2c" {
		t.Errorf("engine was not patched with codex p2c: %d patches", len(patches))
	}
	got, err := e.svc.Strategies(ctx)
	if err != nil || got["codex"] != alias.StrategyP2C || got["grok"] != alias.StrategyRoundRobin || len(got) != len(cliproxyapi.Providers()) {
		t.Errorf("Strategies = %v, %v", got, err)
	}
}

func TestSetStrategyUnknownProvider(t *testing.T) {
	e := newEnv(t)
	e.setClientKey(t)
	ctx := context.Background()
	if err := e.svc.SetStrategy(ctx, "nope", alias.StrategyP2C); !errors.Is(err, ErrUnknownProvider) {
		t.Errorf("err = %v, want ErrUnknownProvider", err)
	}
	if err := e.svc.SetStrategy(ctx, "xai", alias.StrategyP2C); !errors.Is(err, ErrUnknownProvider) {
		t.Errorf("engine id: err = %v, want ErrUnknownProvider", err)
	}
	if got, _ := e.store.AccountStrategies(ctx); len(got) != 0 {
		t.Errorf("stored = %v", got)
	}
}

func TestSetPriority(t *testing.T) {
	e := newEnv(t)
	e.setClientKey(t)
	ctx := context.Background()
	e.fake.AddCredential(cliproxyapi.Credential{Name: "codex-a.json", Provider: "codex"})
	row := e.addRow(t, "codex", "a", "codex-a.json", true)
	if err := e.store.SetAccountStrategy(ctx, "codex", alias.StrategyPriority); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.SetPriority(ctx, row.ID, 4); err != nil {
		t.Fatalf("SetPriority: %v", err)
	}
	if a, _, _ := e.fake.Credential("codex-a.json"); a.Priority != 4 {
		t.Errorf("engine priority = %d, want 4", a.Priority)
	}
	if err := e.svc.SetPriority(ctx, row.ID+50, 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown id: err = %v", err)
	}
}
