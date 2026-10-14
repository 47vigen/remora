package cooling

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/47vigen/remora/internal/alias"
	"github.com/47vigen/remora/internal/engine/bifrost"
)

func failure(code int, retryAfterMS int64) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"status_code":%d,"extra_fields":{"retry_after_ms":%d}}`, code, retryAfterMS))
}

func TestEventFrom(t *testing.T) {
	cases := []struct {
		name string
		in   bifrost.Log
		want Event
		ok   bool
	}{
		{
			name: "402 on an API-key provider",
			in:   bifrost.Log{ID: "r1", Timestamp: t0, Provider: "openrouter", Model: "a", Status: "error", ErrorDetails: failure(402, 0)},
			want: Event{ID: "r1", At: t0, Target: alias.Target{Provider: "openrouter", Model: "a"}, StatusCode: 402}, ok: true,
		},
		{
			name: "429 on the OAuth engine, with a retry hint",
			in:   bifrost.Log{ID: "r2", Timestamp: t0, Provider: "remora-oauth", Model: "codex/gpt-x", Status: "error", ErrorDetails: failure(429, 3000)},
			want: Event{ID: "r2", At: t0, Target: alias.Target{Provider: "codex", Model: "gpt-x"}, StatusCode: 429, RetryAfter: 3 * time.Second},
			ok:   true,
		},
		{
			name: "model with a slash",
			in:   bifrost.Log{ID: "r3", Timestamp: t0, Provider: "openrouter", Model: "vendor/m", Status: "error", ErrorDetails: failure(402, 0)},
			want: Event{ID: "r3", At: t0, Target: alias.Target{Provider: "openrouter", Model: "vendor/m"}, StatusCode: 402}, ok: true,
		},
		{name: "success", in: bifrost.Log{ID: "r4", Provider: "openrouter", Model: "a", Status: "success"}},
		{name: "500", in: bifrost.Log{ID: "r5", Provider: "openrouter", Model: "a", Status: "error", ErrorDetails: failure(500, 0)}},
		{name: "no details", in: bifrost.Log{ID: "r6", Provider: "openrouter", Model: "a", Status: "error"}},
		{name: "no provider", in: bifrost.Log{ID: "r7", Model: "a", Status: "error", ErrorDetails: failure(402, 0)}},
		{name: "OAuth model without prefix", in: bifrost.Log{ID: "r8", Provider: "remora-oauth", Model: "gpt-x", Status: "error", ErrorDetails: failure(402, 0)}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := EventFrom(c.in)
			if ok != c.ok || (ok && got != c.want) {
				t.Fatalf("EventFrom = %+v, %v; want %+v, %v", got, ok, c.want, c.ok)
			}
		})
	}
}

// details is a fake GetLog: it answers each id from a map and counts the calls.
type details struct {
	logs  map[string]bifrost.Log
	calls chan string
}

func (d *details) get(_ context.Context, id string) (bifrost.Log, error) {
	d.calls <- id
	l, ok := d.logs[id]
	if !ok {
		return bifrost.Log{}, errors.New("not found")
	}
	return l, nil
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func startRun(t *testing.T, cl *Cooler, rows chan bifrost.Log, d *details) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- cl.Run(ctx, rows, d.get, 10*time.Millisecond) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Errorf("Run = %v, want context.Canceled", err)
		}
	})
}

func TestRunObservesAndExpires(t *testing.T) {
	cl, k, p := newCooler(t)
	at := t0.Add(time.Second)
	d := &details{calls: make(chan string, 8), logs: map[string]bifrost.Log{
		"r1": {ID: "r1", Timestamp: at, Provider: "or", Model: "a", Status: "error", ErrorDetails: failure(402, 0)},
	}}
	rows := make(chan bifrost.Log, 8)
	startRun(t, cl, rows, d)

	// A failed detail fetch is skipped; a success row is never fetched.
	rows <- bifrost.Log{ID: "gone", Timestamp: at, Provider: "or", Model: "b", Status: "error"}
	rows <- bifrost.Log{ID: "ok", Timestamp: at, Provider: "or", Model: "b", Status: "success"}
	rows <- bifrost.Log{ID: "r1", Timestamp: at, Provider: "or", Model: "a", Status: "error"}
	waitFor(t, "member cooling", func() bool { return len(cl.Snapshot()) == 1 })
	if got := cl.Snapshot()[0]; got.Target != a || got.Reason != "402" {
		t.Fatalf("Snapshot = %+v", cl.Snapshot())
	}
	if n := len(d.calls); n != 2 {
		t.Errorf("detail calls = %d, want 2 (gone, r1)", n)
	}

	k.Set(t0.Add(5*time.Minute + time.Second))
	waitFor(t, "member restored", func() bool { return len(cl.Snapshot()) == 0 })
	if p.count() < 2 {
		t.Errorf("apply calls = %d, want cooling and restore", p.count())
	}
}

// TestRunSkipsRowsBeforeStart: the log feed replays recent rows when it starts, so a 402 (or a 429 burst) from before
// a restart must not cool its member again for a full cooldown.
func TestRunSkipsRowsBeforeStart(t *testing.T) {
	cl, _, _ := newCooler(t) // Run starts at t0
	old := t0.Add(-2 * time.Minute)
	logs := map[string]bifrost.Log{}
	rows := make(chan bifrost.Log, 16)
	push := func(id string, at time.Time, target alias.Target, code int) {
		logs[id] = bifrost.Log{ID: id, Timestamp: at, Provider: target.Provider, Model: target.Model, Status: "error", ErrorDetails: failure(code, 0)}
		rows <- bifrost.Log{ID: id, Timestamp: at, Provider: target.Provider, Model: target.Model, Status: "error"}
	}
	push("old402", old, a, 402)
	for i := range 3 {
		push(fmt.Sprintf("old429-%d", i), old.Add(time.Duration(i)*time.Second), b, 429)
	}
	push("new402", t0.Add(time.Second), c, 402)
	d := &details{calls: make(chan string, 16), logs: logs}
	startRun(t, cl, rows, d)

	waitFor(t, "new member cooling", func() bool { return len(cl.Snapshot()) > 0 })
	time.Sleep(50 * time.Millisecond)
	snap := cl.Snapshot()
	if len(snap) != 1 || snap[0].Target != c {
		t.Fatalf("Snapshot = %+v, want only %+v", snap, c)
	}
	if n := len(d.calls); n != 1 {
		t.Errorf("detail calls = %d, want 1 (old rows are not fetched)", n)
	}
}

func TestRunEndsWhenFeedCloses(t *testing.T) {
	cl, _, _ := newCooler(t)
	rows := make(chan bifrost.Log)
	close(rows)
	d := &details{calls: make(chan string, 1)}
	err := cl.Run(context.Background(), rows, d.get, time.Second)
	if !errors.Is(err, ErrFeedClosed) {
		t.Fatalf("Run = %v, want ErrFeedClosed", err)
	}
}
