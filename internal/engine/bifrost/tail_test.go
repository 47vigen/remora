package bifrost

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// fakeLogs answers SearchLogs from a script: each call takes the next window of rows (repeating the last one once the
// script runs out) and pages it by Offset and Limit, sorted by timestamp ascending.
type fakeLogs struct {
	mu    sync.Mutex
	polls [][]Log // one row set per poll; a poll ends when a page comes back short
	poll  int
	calls []LogFilter
	byID  map[string]Log // what GetLog answers; a missing id is ErrNotFound
	fails func(call int) bool
}

func (f *fakeLogs) GetLog(_ context.Context, id string) (Log, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	l, ok := f.byID[id]
	if !ok {
		return Log{}, ErrNotFound
	}
	return l, nil
}

func (f *fakeLogs) SearchLogs(_ context.Context, lf LogFilter) (LogPage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, lf)
	if f.fails != nil && f.fails(len(f.calls)) {
		f.poll++
		return LogPage{}, io.ErrUnexpectedEOF
	}
	if len(f.polls) == 0 {
		return LogPage{}, nil
	}
	i := min(f.poll, len(f.polls)-1)
	rows := append([]Log(nil), f.polls[i]...)
	sort.SliceStable(rows, func(a, b int) bool { return rows[a].Timestamp.Before(rows[b].Timestamp) })
	if lf.Start != nil {
		rows = rows[sort.Search(len(rows), func(i int) bool { return !rows[i].Timestamp.Before(*lf.Start) }):]
	}
	end := min(lf.Offset+lf.Limit, len(rows))
	var page []Log
	if lf.Offset < len(rows) {
		page = rows[lf.Offset:end]
	}
	if len(page) < lf.Limit {
		f.poll++
	}
	return LogPage{Logs: page, Total: int64(len(rows))}, nil
}

func (f *fakeLogs) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

var t0 = time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)

func row(id string, sec int, status string) Log {
	return Log{ID: id, Timestamp: t0.Add(time.Duration(sec) * time.Second), Provider: "p", Model: "m", Status: status}
}

// drain reads everything ready on ch without blocking.
func drain(ch <-chan Log) []string {
	var ids []string
	for {
		select {
		case l, ok := <-ch:
			if !ok {
				return ids
			}
			ids = append(ids, l.ID+":"+l.Status)
		default:
			return ids
		}
	}
}

func eq(a, b []string) bool { return fmt.Sprint(a) == fmt.Sprint(b) }

func TestTailDeliversOnceInOrder(t *testing.T) {
	src := &fakeLogs{polls: [][]Log{
		{row("a", 1, "error"), row("b", 2, "success")},
		{row("b", 2, "success"), row("c", 3, "error")},
	}}
	tl := NewTail(src, time.Second, nil, nil)
	tl.now = func() time.Time { return t0 }
	ch, cancel := tl.Subscribe(context.Background(), 16, 0)
	defer cancel()
	ctx := context.Background()
	tl.poll(ctx)
	tl.poll(ctx)
	if got := drain(ch); !eq(got, []string{"a:error", "b:success", "c:error"}) {
		t.Fatalf("delivered %v", got)
	}
	f := src.calls[len(src.calls)-1]
	if f.SortBy != "timestamp" || f.Order != "asc" || f.Start == nil || len(f.Status) != 0 {
		t.Fatalf("filter = %+v", f)
	}
	if want := t0.Add(2 * time.Second).Add(-defaultLookback); !f.Start.Equal(want) {
		t.Fatalf("second poll start = %v, want %v", f.Start, want)
	}
}

func TestTailSearchesWithoutContent(t *testing.T) {
	for _, mode := range []string{"cooling", "live"} {
		t.Run(mode, func(t *testing.T) {
			src := &fakeLogs{}
			tail := NewTail(src, time.Second, []string{"error"}, nil)
			if mode == "live" {
				tail = NewTail(src, time.Second, nil, nil).Live()
				_, cancel := tail.Subscribe(t.Context(), 1, 0)
				defer cancel()
			} else {
				tail.poll(t.Context())
			}
			if len(src.calls) != 1 || !src.calls[0].NoContent {
				t.Fatalf("tail search = %+v, want a metadata-only search", src.calls)
			}
		})
	}
}

func TestTailCatchesLateRow(t *testing.T) {
	src := &fakeLogs{polls: [][]Log{
		{row("b", 300, "error")},
		{row("a", 1, "error"), row("b", 300, "error")},
	}}
	tl := NewTail(src, time.Second, nil, nil)
	tl.now = func() time.Time { return t0 }
	ch, cancel := tl.Subscribe(context.Background(), 16, 0)
	defer cancel()
	ctx := context.Background()
	tl.poll(ctx)
	tl.poll(ctx)
	if got := drain(ch); !eq(got, []string{"b:error", "a:error"}) {
		t.Fatalf("delivered %v", got)
	}
	if start := src.calls[1].Start; !start.Before(t0.Add(time.Second)) {
		t.Fatalf("second poll start %v does not reach back to the late row", start)
	}
}

func TestTailSkipsProcessing(t *testing.T) {
	src := &fakeLogs{polls: [][]Log{
		{row("a", 1, "processing")},
		{row("a", 1, "error")},
		{row("a", 1, "error")},
	}}
	tl := NewTail(src, time.Second, nil, nil)
	tl.now = func() time.Time { return t0 }
	ch, cancel := tl.Subscribe(context.Background(), 16, 0)
	defer cancel()
	ctx := context.Background()
	tl.poll(ctx)
	if got := drain(ch); len(got) != 0 {
		t.Fatalf("processing row delivered: %v", got)
	}
	tl.poll(ctx)
	tl.poll(ctx)
	if got := drain(ch); !eq(got, []string{"a:error"}) {
		t.Fatalf("delivered %v", got)
	}
}

func TestTailPagesBeyondLimit(t *testing.T) {
	rows := make([]Log, 1500)
	for i := range rows {
		rows[i] = Log{ID: fmt.Sprintf("r%04d", i), Timestamp: t0.Add(time.Duration(i) * time.Millisecond), Status: "success"}
	}
	src := &fakeLogs{polls: [][]Log{rows}}
	tl := NewTail(src, time.Second, nil, nil)
	tl.now = func() time.Time { return t0 }
	ch, cancel := tl.Subscribe(context.Background(), 4000, 0)
	defer cancel()
	ctx := context.Background()
	tl.poll(ctx)
	tl.poll(ctx)
	got := drain(ch)
	if len(got) != 1500 {
		t.Fatalf("delivered %d rows, want 1500", len(got))
	}
	seen := map[string]bool{}
	for _, id := range got {
		if seen[id] {
			t.Fatalf("%s delivered twice", id)
		}
		seen[id] = true
	}
	if len(src.calls) < 2 || src.calls[0].Offset != 0 || src.calls[1].Offset != src.calls[0].Limit {
		t.Fatalf("offsets: %+v", src.calls)
	}
	// Offset pagination stays anchored within a poll, even as it records progress for a retry.
	if !src.calls[0].Start.Equal(*src.calls[1].Start) {
		t.Fatalf("start moved mid-window: %v then %v", src.calls[0].Start, src.calls[1].Start)
	}
}

func TestTailFailedPageKeepsProgress(t *testing.T) {
	rows := make([]Log, 201)
	for i := range rows {
		rows[i] = row(fmt.Sprintf("r%03d", i), i-200, "success")
	}
	rows[200].Timestamp = rows[199].Timestamp // the retry must include ties without redelivery
	src := &fakeLogs{polls: [][]Log{rows}, fails: func(call int) bool { return call == 2 }}
	tl := NewTail(src, time.Second, nil, nil)
	tl.now = func() time.Time { return t0 }
	tl.cursor = t0 // the overlap can deliver rows older than the previous poll's cursor
	ctx := context.Background()
	ch, cancel := tl.Subscribe(ctx, len(rows), 0)
	defer cancel()

	tl.poll(ctx)
	if got := drain(ch); len(got) != 200 {
		t.Fatalf("delivered %d rows before the failed page, want 200", len(got))
	}
	tl.poll(ctx)
	if f := src.calls[2]; f.Offset != 0 || !f.Start.Equal(rows[199].Timestamp) {
		t.Fatalf("retry starts at %v (offset %d), want %v (offset 0)", f.Start, f.Offset, rows[199].Timestamp)
	}
	if got := drain(ch); !eq(got, []string{"r200:success"}) {
		t.Fatalf("retry delivered %v, want only the unread row", got)
	}
}

func TestTailWindowClamped(t *testing.T) {
	src := &fakeLogs{fails: func(call int) bool { return call > 1 }}
	const interval = 2 * time.Second
	tl := NewTail(src, interval, nil, nil)
	now := t0
	tl.now = func() time.Time { return now }
	ctx := context.Background()
	tl.poll(ctx)
	tl.seen["old"] = now
	now = now.Add(interval)
	tl.poll(ctx)
	now = now.Add(time.Hour)
	tl.poll(ctx)
	if got, want := src.calls[2].Start, now.Add(-defaultLookback-interval); !got.Equal(want) {
		t.Fatalf("start = %v after an hour of failures, want %v", got, want)
	}
	if len(tl.seen) != 0 {
		t.Fatal("deduplication retained rows outside the clamped window")
	}
}

func TestTailBackoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const interval = 250 * time.Millisecond
		var attempts []time.Time
		src := &fakeLogs{fails: func(call int) bool {
			attempts = append(attempts, time.Now())
			return call <= 9 || call == 12
		}}
		var logs bytes.Buffer
		tl := NewTail(src, interval, nil, slog.New(slog.NewJSONHandler(&logs, nil)))
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		_, unsubscribe := tl.Subscribe(ctx, 1, 0)
		defer unsubscribe()
		done := make(chan struct{})
		start := time.Now()
		go func() { tl.Run(ctx); close(done) }()

		gaps := []time.Duration{
			interval, time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second,
			16 * time.Second, 32 * time.Second, time.Minute, time.Minute, time.Minute,
			interval, interval, time.Second, interval,
		}
		var elapsed time.Duration
		for _, gap := range gaps {
			elapsed += gap
		}
		time.Sleep(elapsed + time.Nanosecond)
		cancel()
		<-done

		if len(attempts) != len(gaps) {
			t.Fatalf("polled %d times over %v, want %d", len(attempts), elapsed, len(gaps))
		}
		for i, attempt := range attempts {
			if got := attempt.Sub(start); got != gaps[i] {
				t.Errorf("poll %d after %v, want %v", i+1, got, gaps[i])
			}
			start = attempt
		}
		if got := strings.Count(logs.String(), `"level":"WARN"`); got != 8 {
			t.Errorf("warnings = %d, want one per backoff step plus one after reset", got)
		}
		if got := strings.Count(logs.String(), `"level":"INFO"`); got != 2 {
			t.Errorf("info messages = %d, want one per recovery", got)
		}
	})
}

func TestTailPageSize(t *testing.T) {
	src := &fakeLogs{polls: [][]Log{make([]Log, 201)}}
	for i := range src.polls[0] {
		src.polls[0][i] = row("row", i, "success")
	}
	tl := NewTail(src, time.Second, nil, nil)
	tl.now = func() time.Time { return t0 }
	tl.poll(context.Background())
	if len(src.calls) != 2 || src.calls[0].Limit != 200 || src.calls[1].Limit != 200 {
		t.Fatalf("requested pages %+v, want two with limit 200", src.calls)
	}
}

func TestTailIdleWithoutSubscribers(t *testing.T) {
	src := &fakeLogs{polls: [][]Log{{row("a", 1, "error")}}}
	const interval = 5 * time.Millisecond
	tl := NewTail(src, interval, nil, nil)
	tl.now = func() time.Time { return t0 }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { tl.Run(ctx); close(done) }()
	time.Sleep(10 * interval)
	if n := src.callCount(); n != 0 {
		t.Fatalf("SearchLogs called %d times without subscribers", n)
	}

	ch, unsubscribe := tl.Subscribe(context.Background(), 4, 0)
	select {
	case l := <-ch:
		if l.ID != "a" {
			t.Fatalf("delivered %+v", l)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no row delivered once subscribed")
	}
	unsubscribe()
	unsubscribe() // idempotent
	time.Sleep(2 * interval)
	n := src.callCount()
	time.Sleep(10 * interval)
	if after := src.callCount(); after != n {
		t.Fatalf("SearchLogs called %d more times after the last subscriber left", after-n)
	}
	cancel()
	<-done
}

func TestTailKeepsSlowSubscriber(t *testing.T) {
	src := &fakeLogs{polls: [][]Log{{row("a", 1, "error"), row("b", 2, "error"), row("c", 3, "error")}}}
	tl := NewTail(src, time.Second, nil, nil)
	tl.now = func() time.Time { return t0 }
	slow, cancelSlow := tl.Subscribe(context.Background(), 1, 2)
	defer cancelSlow()
	fast, cancelFast := tl.Subscribe(context.Background(), 16, 0)
	defer cancelFast()
	ctx := context.Background()
	tl.poll(ctx)

	if got := drain(fast); !eq(got, []string{"a:error", "b:error", "c:error"}) {
		t.Fatalf("fast subscriber got %v", got)
	}
	var got []string
	for range 3 {
		got = append(got, drain(slow)...)
		tl.poll(ctx)
	}
	if !eq(got, []string{"a:error", "b:error", "c:error"}) {
		t.Fatalf("slow subscriber got %v", got)
	}
}

func TestTailDropsSubscriberPastBacklog(t *testing.T) {
	src := &fakeLogs{polls: [][]Log{{row("a", 1, "error"), row("b", 2, "error"), row("c", 3, "error")}}}
	tl := NewTail(src, time.Second, nil, nil)
	tl.now = func() time.Time { return t0 }
	slow, cancelSlow := tl.Subscribe(context.Background(), 1, 1)
	tl.poll(context.Background())

	if l, ok := <-slow; !ok || l.ID != "a" {
		t.Fatalf("slow subscriber first read = %+v, %v", l, ok)
	}
	if _, ok := <-slow; ok {
		t.Fatal("slow subscriber's channel is still open")
	}
	cancelSlow() // after a drop, cancel must not panic on a closed channel
}

func TestTailCatchesLongRequest(t *testing.T) {
	src := &fakeLogs{
		polls: [][]Log{
			{row("a", 1, "processing"), row("gone", 2, "processing")},
			{row("b", 1000, "success")}, // a and gone are now behind the lookback
		},
		byID: map[string]Log{"a": row("a", 1, "processing")},
	}
	tl := NewTail(src, time.Second, nil, nil)
	tl.now = func() time.Time { return t0 }
	ch, cancel := tl.Subscribe(context.Background(), 16, 0)
	defer cancel()
	ctx := context.Background()
	tl.poll(ctx)
	tl.poll(ctx)
	if got := drain(ch); !eq(got, []string{"b:success"}) {
		t.Fatalf("delivered %v", got)
	}
	src.mu.Lock()
	src.byID["a"] = row("a", 1, "error")
	src.mu.Unlock()
	tl.poll(ctx)
	tl.poll(ctx)
	if got := drain(ch); !eq(got, []string{"a:error"}) {
		t.Fatalf("delivered %v", got)
	}
	if len(tl.running) != 0 {
		t.Fatalf("still tracking %v", tl.running)
	}
}

func TestTailStatusFilter(t *testing.T) {
	src := &fakeLogs{polls: [][]Log{{row("a", 1, "error")}}}
	tl := NewTail(src, time.Second, []string{"error"}, nil)
	tl.now = func() time.Time { return t0 }
	_, cancel := tl.Subscribe(context.Background(), 16, 0)
	defer cancel()
	tl.poll(context.Background())
	if len(src.calls) == 0 {
		t.Fatal("no SearchLogs call")
	}
	for _, f := range src.calls {
		if !eq(f.Status, []string{"error"}) {
			t.Fatalf("filter status = %v, want [error]", f.Status)
		}
	}
}

func TestTailAdvancesWithoutRows(t *testing.T) {
	src := &fakeLogs{polls: [][]Log{{row("a", 1, "error")}, {}}}
	tl := NewTail(src, time.Second, []string{"error"}, nil)
	clock := t0.Add(time.Second)
	tl.now = func() time.Time { return clock }
	_, cancel := tl.Subscribe(context.Background(), 16, 0)
	defer cancel()
	ctx := context.Background()
	tl.poll(ctx)
	clock = t0.Add(24 * time.Hour)
	tl.poll(ctx)
	tl.poll(ctx)
	if want := clock.Add(-defaultLookback); !src.calls[2].Start.Equal(want) {
		t.Fatalf("third poll start = %v, want %v: an idle feed must not keep reading from its last row", src.calls[2].Start, want)
	}
}

// A viewer who opens the log after an idle spell gets what completes from then on, not the minutes before it, which
// its page already shows. The existing rows are read before the viewer is registered, so a request that completes
// between the subscription and the first poll is delivered, not taken for history.
func TestLiveTailSkipsHistoryButNotCompletionsAfterSubscribe(t *testing.T) {
	src := &fakeLogs{polls: [][]Log{
		{row("old1", 1, "success"), row("old2", 2, "success")},
		{row("old1", 1, "success"), row("old2", 2, "success"), row("new", 3, "success")},
	}}
	tl := NewTail(src, time.Second, nil, nil).Live()
	tl.now = func() time.Time { return t0.Add(10 * time.Second) }
	ch, cancel := tl.Subscribe(context.Background(), 16, 0)
	defer cancel()
	if src.callCount() != 1 {
		t.Fatalf("Subscribe read the log %d times, want 1", src.callCount())
	}
	if got := drain(ch); len(got) != 0 {
		t.Fatalf("history delivered: %v", got)
	}
	tl.poll(context.Background())
	if got := drain(ch); !eq(got, []string{"new:success"}) {
		t.Fatalf("delivered %v", got)
	}
}

func TestLiveTailPrimesAfterFailedPrime(t *testing.T) {
	// A failed priming read leaves the tail unprimed: its first successful poll only primes, so a viewer never gets
	// the lookback replayed (the panel reads its own catch-up page on connect); rows that complete after that arrive.
	src := &fakeLogs{
		polls: [][]Log{{row("old", 1, "success")}, {row("old", 1, "success")}, {row("old", 1, "success"), row("new", 2, "success")}},
		fails: func(call int) bool { return call == 1 },
	}
	tl := NewTail(src, time.Second, nil, nil).Live()
	tl.now = func() time.Time { return t0 }
	ctx := context.Background()
	ch, cancel := tl.Subscribe(ctx, 16, 0)
	defer cancel()
	_, cancelSecond := tl.Subscribe(ctx, 16, 0)
	defer cancelSecond()
	if got := src.callCount(); got != 1 {
		t.Fatalf("Subscribe read the log %d times during backoff, want 1", got)
	}
	tl.poll(ctx)
	if got := drain(ch); len(got) != 0 {
		t.Fatalf("first successful poll after a failed prime delivered %v, want nothing", got)
	}
	tl.poll(ctx)
	if got := drain(ch); !eq(got, []string{"new:success"}) {
		t.Fatalf("second poll delivered %v, want [new:success]", got)
	}
}

// Most live polls read the last minute; every tenth reads the whole lookback so a long request is found.
func TestLiveTailReadsNarrowWindowMostPolls(t *testing.T) {
	src := &fakeLogs{}
	tl := NewTail(src, time.Second, nil, nil).Live()
	now := t0
	var nows []time.Time
	tl.now = func() time.Time { nows = append(nows, now); return now }
	_, cancel := tl.Subscribe(context.Background(), 16, 0) // reads the whole lookback once, to prime the tail
	defer cancel()
	ctx := context.Background()
	for range 2 * liveWideEvery {
		now = now.Add(time.Second)
		tl.poll(ctx)
	}
	wide := 0
	for i, c := range src.calls {
		back := nows[i].Sub(*c.Start)
		if back >= defaultLookback {
			wide++
		} else if back > liveNarrow+time.Second {
			t.Fatalf("poll %d reaches back %v", i, back)
		}
	}
	if wide != 3 {
		t.Fatalf("%d of %d polls read the whole lookback, want the priming read and 2", wide, len(src.calls))
	}
}
