package bifrost

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"
)

// fakeLogs answers SearchLogs from a script: each call takes the next window of rows (repeating the last one once the
// script runs out) and pages it by Offset and Limit, sorted by timestamp ascending.
type fakeLogs struct {
	mu    sync.Mutex
	polls [][]Log // one row set per poll; a poll ends when a page comes back short
	poll  int
	calls []LogFilter
}

func (f *fakeLogs) SearchLogs(_ context.Context, lf LogFilter) (LogPage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, lf)
	if len(f.polls) == 0 {
		return LogPage{}, nil
	}
	i := min(f.poll, len(f.polls)-1)
	rows := append([]Log(nil), f.polls[i]...)
	sort.SliceStable(rows, func(a, b int) bool { return rows[a].Timestamp.Before(rows[b].Timestamp) })
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
	tl := NewTail(src, time.Second, nil)
	ch, cancel := tl.Subscribe(16)
	defer cancel()
	ctx := context.Background()
	tl.poll(ctx)
	tl.poll(ctx)
	if got := drain(ch); !eq(got, []string{"a:error", "b:success", "c:error"}) {
		t.Fatalf("delivered %v", got)
	}
	f := src.calls[len(src.calls)-1]
	if f.SortBy != "timestamp" || f.Order != "asc" || f.Limit != 1000 || f.Start == nil || len(f.Status) != 0 {
		t.Fatalf("filter = %+v", f)
	}
	if want := t0.Add(2 * time.Second).Add(-defaultLookback); !f.Start.Equal(want) {
		t.Fatalf("second poll start = %v, want %v", f.Start, want)
	}
}

func TestTailCatchesLateRow(t *testing.T) {
	src := &fakeLogs{polls: [][]Log{
		{row("b", 300, "error")},
		{row("a", 1, "error"), row("b", 300, "error")},
	}}
	tl := NewTail(src, time.Second, nil)
	ch, cancel := tl.Subscribe(16)
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
	tl := NewTail(src, time.Second, nil)
	ch, cancel := tl.Subscribe(16)
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
	tl := NewTail(src, time.Second, nil)
	ch, cancel := tl.Subscribe(4000)
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
	if len(src.calls) < 2 || src.calls[0].Offset != 0 || src.calls[1].Offset != 1000 {
		t.Fatalf("offsets: %+v", src.calls)
	}
	// The cursor moves only once the window is fully read: both pages of the first poll share one start.
	if !src.calls[0].Start.Equal(*src.calls[1].Start) {
		t.Fatalf("start moved mid-window: %v then %v", src.calls[0].Start, src.calls[1].Start)
	}
}

func TestTailIdleWithoutSubscribers(t *testing.T) {
	src := &fakeLogs{polls: [][]Log{{row("a", 1, "error")}}}
	const interval = 5 * time.Millisecond
	tl := NewTail(src, interval, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { tl.Run(ctx); close(done) }()
	time.Sleep(10 * interval)
	if n := src.callCount(); n != 0 {
		t.Fatalf("SearchLogs called %d times without subscribers", n)
	}

	ch, unsubscribe := tl.Subscribe(4)
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

func TestTailDropsSlowSubscriber(t *testing.T) {
	src := &fakeLogs{polls: [][]Log{{row("a", 1, "error"), row("b", 2, "error"), row("c", 3, "error")}}}
	tl := NewTail(src, time.Second, nil)
	slow, cancelSlow := tl.Subscribe(1)
	fast, cancelFast := tl.Subscribe(16)
	defer cancelFast()
	tl.poll(context.Background())

	if got := drain(fast); !eq(got, []string{"a:error", "b:error", "c:error"}) {
		t.Fatalf("fast subscriber got %v", got)
	}
	if l, ok := <-slow; !ok || l.ID != "a" {
		t.Fatalf("slow subscriber first read = %+v, %v", l, ok)
	}
	if _, ok := <-slow; ok {
		t.Fatal("slow subscriber's channel is still open")
	}
	cancelSlow() // after a drop, cancel must not panic on a closed channel
}
