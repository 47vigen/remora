package cooling

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/47vigen/remora/internal/alias"
)

var (
	t0 = time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	a  = alias.Target{Provider: "or", Model: "a"}
	b  = alias.Target{Provider: "or", Model: "b"}
	c  = alias.Target{Provider: "or", Model: "c"}
)

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (k *clock) Now() time.Time {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.now
}

func (k *clock) Set(t time.Time) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.now = t
}

type applier struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (p *applier) apply(context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	return p.err
}

func (p *applier) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func newCooler(t *testing.T) (*Cooler, *clock, *applier) {
	t.Helper()
	k := &clock{now: t0}
	p := &applier{}
	return New(Config{}, p.apply, k.Now, nil), k, p
}

func ev(target alias.Target, code int, at time.Time) Event {
	return Event{ID: at.String(), At: at, Target: target, StatusCode: code}
}

func TestCoolsOnlyFailingMember(t *testing.T) {
	cl, _, p := newCooler(t)
	if err := cl.Observe(context.Background(), ev(a, 402, t0)); err != nil {
		t.Fatal(err)
	}
	want := []Cooling{{Target: a, Until: t0.Add(5 * time.Minute), Reason: "402"}}
	if got := cl.Snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Snapshot = %+v, want %+v", got, want)
	}
	if p.count() != 1 {
		t.Fatalf("apply called %d times, want 1", p.count())
	}
	// Another 402 on a member that is already cooling with no later deadline changes nothing.
	if err := cl.Observe(context.Background(), ev(a, 402, t0)); err != nil {
		t.Fatal(err)
	}
	if p.count() != 1 {
		t.Fatalf("apply called again for a member already cooling")
	}
}

func TestRetryAfterHonoured(t *testing.T) {
	cl, k, p := newCooler(t)
	e := ev(a, 402, t0)
	e.RetryAfter = 30 * time.Second
	if err := cl.Observe(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if got := cl.Snapshot(); len(got) != 1 || !got[0].Until.Equal(t0.Add(30*time.Second)) {
		t.Fatalf("Snapshot = %+v", got)
	}
	// A later event with a later deadline extends Until; an earlier one does not shorten it.
	k.Set(t0.Add(10 * time.Second))
	e = ev(a, 429, t0.Add(10*time.Second))
	for range 3 {
		e.RetryAfter = time.Minute
		if err := cl.Observe(context.Background(), e); err != nil {
			t.Fatal(err)
		}
	}
	got := cl.Snapshot()
	if len(got) != 1 || !got[0].Until.Equal(t0.Add(70*time.Second)) || got[0].Reason != "429" {
		t.Fatalf("after extension Snapshot = %+v", got)
	}
	e = ev(a, 402, t0.Add(10*time.Second))
	e.RetryAfter = time.Second
	if err := cl.Observe(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if got := cl.Snapshot(); !got[0].Until.Equal(t0.Add(70 * time.Second)) {
		t.Fatalf("an earlier deadline shortened Until: %+v", got)
	}
	if p.count() != 1 {
		t.Fatalf("apply called %d times; extending a cooling member changes no chain", p.count())
	}
}

func Test429Threshold(t *testing.T) {
	tests := []struct {
		name    string
		offsets []time.Duration
		cooling bool
	}{
		{"two within the window", []time.Duration{0, 10 * time.Second}, false},
		{"three within the window", []time.Duration{0, 20 * time.Second, 59 * time.Second}, true},
		{"three spread over more than the window", []time.Duration{0, 31 * time.Second, 61 * time.Second}, false},
		{"three arriving out of order", []time.Duration{50 * time.Second, 0, 20 * time.Second}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cl, _, p := newCooler(t)
			for _, off := range tt.offsets {
				if err := cl.Observe(context.Background(), ev(a, 429, t0.Add(off))); err != nil {
					t.Fatal(err)
				}
			}
			got := cl.Snapshot()
			if !tt.cooling {
				if len(got) != 0 || p.count() != 0 {
					t.Fatalf("Snapshot = %+v, apply calls %d; want nothing", got, p.count())
				}
				return
			}
			want := []Cooling{{Target: a, Until: t0.Add(5 * time.Minute), Reason: "429"}}
			if !reflect.DeepEqual(got, want) || p.count() != 1 {
				t.Fatalf("Snapshot = %+v, apply calls %d; want %+v once", got, p.count(), want)
			}
		})
	}
}

func TestExpireRestores(t *testing.T) {
	cl, k, p := newCooler(t)
	ctx := context.Background()
	for i := range 3 {
		if err := cl.Observe(ctx, ev(a, 429, t0.Add(time.Duration(i)*time.Second))); err != nil {
			t.Fatal(err)
		}
	}
	until := t0.Add(5 * time.Minute)

	k.Set(until.Add(-time.Second))
	if err := cl.Expire(ctx); err != nil {
		t.Fatal(err)
	}
	if len(cl.Snapshot()) != 1 || p.count() != 1 {
		t.Fatalf("restored early: %+v, apply calls %d", cl.Snapshot(), p.count())
	}

	k.Set(until.Add(time.Second))
	if err := cl.Expire(ctx); err != nil {
		t.Fatal(err)
	}
	if got := cl.Snapshot(); len(got) != 0 {
		t.Fatalf("Snapshot after expiry = %+v", got)
	}
	if p.count() != 2 {
		t.Fatalf("apply calls = %d, want 2", p.count())
	}
	if err := cl.Expire(ctx); err != nil {
		t.Fatal(err)
	}
	if p.count() != 2 {
		t.Fatalf("Expire with nothing to restore called apply")
	}

	// The 429 count was reset: two more inside the old window do not cool the member again.
	for i := range 2 {
		if err := cl.Observe(ctx, ev(a, 429, t0.Add(time.Duration(10+i)*time.Second))); err != nil {
			t.Fatal(err)
		}
	}
	if got := cl.Snapshot(); len(got) != 0 {
		t.Fatalf("count not reset on restore: %+v", got)
	}
}

func TestFilterDropsCooling(t *testing.T) {
	cl, _, _ := newCooler(t)
	if err := cl.Observe(context.Background(), ev(b, 402, t0)); err != nil {
		t.Fatal(err)
	}
	if got, want := cl.Filter([]alias.Target{a, b, c}), []alias.Target{a, c}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Filter = %v, want %v", got, want)
	}
}

func TestFilterKeepsChainWhenAllCooling(t *testing.T) {
	cl, _, _ := newCooler(t)
	for _, m := range []alias.Target{a, b} {
		if err := cl.Observe(context.Background(), ev(m, 402, t0)); err != nil {
			t.Fatal(err)
		}
	}
	chain := []alias.Target{a, b}
	if got := cl.Filter(chain); !reflect.DeepEqual(got, chain) {
		t.Fatalf("Filter = %v, want the full chain %v", got, chain)
	}
	if got := cl.Filter(nil); len(got) != 0 {
		t.Fatalf("Filter(nil) = %v", got)
	}
}

func TestIgnoresOtherStatus(t *testing.T) {
	cl, _, p := newCooler(t)
	for i, code := range []int{500, 401, 500, 401, 200, 0} {
		if err := cl.Observe(context.Background(), ev(a, code, t0.Add(time.Duration(i)*time.Second))); err != nil {
			t.Fatal(err)
		}
	}
	if got := cl.Snapshot(); len(got) != 0 || p.count() != 0 {
		t.Fatalf("Snapshot = %+v, apply calls %d", got, p.count())
	}
}

func TestApplyFailureKeepsState(t *testing.T) {
	cl, k, p := newCooler(t)
	p.err = errors.New("front door down")
	ctx := context.Background()
	if err := cl.Observe(ctx, ev(a, 402, t0)); !errors.Is(err, p.err) {
		t.Fatalf("Observe = %v, want the apply error", err)
	}
	if got := cl.Snapshot(); len(got) != 1 || got[0].Target != a {
		t.Fatalf("Snapshot = %+v, want a still cooling", got)
	}
	// The next Expire retries the failed apply even though nothing expired.
	p.err = nil
	k.Set(t0.Add(time.Minute))
	if err := cl.Expire(ctx); err != nil {
		t.Fatal(err)
	}
	if p.count() != 2 {
		t.Fatalf("apply calls = %d, want a retry", p.count())
	}
	if err := cl.Expire(ctx); err != nil {
		t.Fatal(err)
	}
	if p.count() != 2 {
		t.Fatalf("apply retried after it succeeded")
	}
}

func TestSnapshotSortedByUntil(t *testing.T) {
	cl, _, _ := newCooler(t)
	ctx := context.Background()
	for _, x := range []struct {
		target alias.Target
		retry  time.Duration
	}{{a, 3 * time.Minute}, {b, time.Minute}, {c, 2 * time.Minute}} {
		e := ev(x.target, 402, t0)
		e.RetryAfter = x.retry
		if err := cl.Observe(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	var got []alias.Target
	for _, s := range cl.Snapshot() {
		got = append(got, s.Target)
	}
	if want := []alias.Target{b, c, a}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Snapshot order = %v, want %v", got, want)
	}
}
