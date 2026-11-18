package store

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/47vigen/remora/internal/alias"
	"github.com/47vigen/remora/internal/cooling"
)

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
