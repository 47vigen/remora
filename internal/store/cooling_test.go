package store

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/47vigen/remora/internal/alias"
	"github.com/47vigen/remora/internal/cooling"
)

func TestLoadCoolingKeepsExpiredBreaker(t *testing.T) {
	ctx := t.Context()
	st := openTemp(t)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	breaker := cooling.Cooling{Target: alias.Target{Provider: "up", Model: "breaker"}, Until: now, Reason: "503", Backoff: 30 * time.Second}
	plain := cooling.Cooling{Target: alias.Target{Provider: "up", Model: "plain"}, Until: now, Reason: "429"}
	if err := st.SaveCooling(ctx, []cooling.Cooling{breaker, plain}); err != nil {
		t.Fatal(err)
	}
	got, err := st.LoadCooling(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != breaker {
		t.Fatalf("LoadCooling = %+v, want only %+v", got, breaker)
	}
	var count int
	if err := st.DB().QueryRowContext(ctx, `SELECT count(*) FROM cooling`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("cooling rows after load = %d, %v; want 1", count, err)
	}
}

func TestRestartAfterBreakerDeadlineYieldsOneProbe(t *testing.T) {
	ctx := t.Context()
	st := openTemp(t)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	target := alias.Target{Provider: "up", Model: "breaker"}
	if err := st.SaveCooling(ctx, []cooling.Cooling{
		{Target: target, Until: now.Add(-time.Second), Reason: "503", Backoff: 30 * time.Second},
	}); err != nil {
		t.Fatal(err)
	}
	cl := cooling.New(cooling.Config{}, nil, func() time.Time { return now }, nil)
	if err := cl.Restore(ctx, st); err != nil {
		t.Fatal(err)
	}
	chain := []alias.Target{target}
	type admission struct {
		probe   alias.Target
		allowed bool
	}
	results := make(chan admission, 2)
	for range 2 {
		go func() {
			probe, allowed := cl.Admit(chain)
			results <- admission{probe, allowed}
		}()
	}
	var probes int
	for range 2 {
		switch got := <-results; got {
		case admission{target, true}:
			probes++
		case admission{}:
		default:
			t.Fatalf("unexpected admission before a successful probe: %+v", got)
		}
	}
	if probes != 1 || !cl.HasBreakers() {
		t.Fatalf("restored breaker: got %d probes and HasBreakers=%t; want 1 and true", probes, cl.HasBreakers())
	}
	if err := cl.CompleteProbe(ctx, target, 200, 0); err != nil {
		t.Fatal(err)
	}
	if probe, allowed := cl.Admit(chain); probe != (alias.Target{}) || !allowed || cl.HasBreakers() {
		t.Fatalf("after successful probe: admitted %v, %v; HasBreakers=%t", probe, allowed, cl.HasBreakers())
	}
}

func TestCoolingSurvivesRestart(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "cooling.db")
	open := func() *Store {
		t.Helper()
		st, err := Open(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = st.Close() })
		return st
	}
	now := time.Date(2026, 1, 1, 0, 0, 0, 123, time.UTC)
	clock := func() time.Time { return now }
	st := open()
	cl := cooling.New(cooling.Config{}, nil, clock, nil)
	if err := cl.Restore(ctx, st); err != nil {
		t.Fatal(err)
	}
	short := alias.Target{Provider: "up", Model: "a"}
	long := alias.Target{Provider: "up", Model: "b"}
	breaker := alias.Target{Provider: "up", Model: "c"}
	for _, e := range []cooling.Event{
		{Target: short, At: now, StatusCode: 402, RetryAfter: time.Second},
		{Target: long, At: now, StatusCode: 402, RetryAfter: time.Minute},
		{Target: long, At: now, StatusCode: 402, RetryAfter: 2 * time.Minute},
	} {
		if err := cl.Observe(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	for range 5 {
		if err := cl.Observe(ctx, cooling.Event{Target: breaker, At: now, StatusCode: 503}); err != nil {
			t.Fatal(err)
		}
	}
	want := cl.Snapshot()[1:]
	now = now.Add(2 * time.Second)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st = open()
	cl = cooling.New(cooling.Config{}, nil, clock, nil)
	if err := cl.Restore(ctx, st); err != nil {
		t.Fatal(err)
	}
	if got := cl.Snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("restored %v, want %v", got, want)
	}
	var count int
	if err := st.DB().QueryRowContext(ctx, `SELECT count(*) FROM cooling`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("cooling rows after boot = %d, %v; want 2", count, err)
	}
	now = now.Add(2 * time.Minute)
	if target, allowed := cl.Admit([]alias.Target{breaker}); target != breaker || !allowed {
		t.Fatalf("restored breaker not available for probing: %v, %v", target, allowed)
	}
	if err := cl.CompleteProbe(ctx, breaker, 200, 0); err != nil {
		t.Fatal(err)
	}
	if err := cl.Expire(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.DB().QueryRowContext(ctx, `SELECT count(*) FROM cooling`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("cooling rows after expiry = %d, %v; want 0", count, err)
	}
}
