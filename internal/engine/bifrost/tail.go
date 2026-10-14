package bifrost

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

const (
	defaultTailInterval = time.Second
	// defaultLookback is how far before the newest row seen each poll reaches back. A row is written when its request
	// completes but carries the request's start time, so a long request lands behind rows already seen.
	defaultLookback = 10 * time.Minute
)

// LogSource is what a Tail polls; *Client satisfies it.
type LogSource interface {
	SearchLogs(ctx context.Context, f LogFilter) (LogPage, error)
}

// Tail follows the front door's request log by polling it, since the engine pushes no log rows. It delivers each
// completed row once to every subscriber, polls only while someone is subscribed, and never blocks on a subscriber.
type Tail struct {
	src      LogSource
	interval time.Duration
	lookback time.Duration
	log      *slog.Logger
	now      func() time.Time

	mu     sync.Mutex
	subs   map[int]chan Log
	nextID int

	// Poll state, touched only by the goroutine that polls.
	cursor time.Time            // newest timestamp delivered; zero until a row arrives, when the clock stands in
	seen   map[string]time.Time // delivered row id → its timestamp
}

// NewTail returns a Tail over src that polls every interval (1s when interval ≤ 0). A nil log discards.
func NewTail(src LogSource, interval time.Duration, log *slog.Logger) *Tail {
	if interval <= 0 {
		interval = defaultTailInterval
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Tail{
		src: src, interval: interval, lookback: defaultLookback, log: log, now: time.Now,
		subs: map[int]chan Log{}, seen: map[string]time.Time{},
	}
}

// Subscribe returns a channel of completed rows with room for buffer rows, and a function that ends the
// subscription. A subscriber that lets its buffer fill is dropped: its channel is closed. The cancel function is safe
// to call more than once and after a drop.
func (t *Tail) Subscribe(buffer int) (<-chan Log, func()) {
	if buffer < 1 {
		buffer = 1
	}
	ch := make(chan Log, buffer)
	t.mu.Lock()
	id := t.nextID
	t.nextID++
	t.subs[id] = ch
	t.mu.Unlock()
	return ch, func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		if c, ok := t.subs[id]; ok {
			delete(t.subs, id)
			close(c)
		}
	}
}

// Run polls every interval until ctx is done, skipping ticks while nobody is subscribed.
func (t *Tail) Run(ctx context.Context) {
	tick := time.NewTicker(t.interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if t.subscribers() > 0 {
				t.poll(ctx)
			}
		}
	}
}

func (t *Tail) subscribers() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.subs)
}

// poll reads the whole window from cursor − lookback, page by page, and delivers the rows not seen yet. The cursor
// advances only once every page of the window has been read, so a burst larger than one page is never skipped; a
// failed page leaves the cursor where it was and the next poll reads the window again.
func (t *Tail) poll(ctx context.Context) {
	from := t.cursor
	if from.IsZero() {
		from = t.now()
	}
	start := from.Add(-t.lookback)
	newest := t.cursor
	for offset := 0; ; offset += MaxLogLimit {
		page, err := t.src.SearchLogs(ctx, LogFilter{
			Start: &start, Limit: MaxLogLimit, Offset: offset, SortBy: "timestamp", Order: "asc",
		})
		if err != nil {
			if ctx.Err() == nil {
				t.log.WarnContext(ctx, "reading the request log failed", "err", err)
			}
			return
		}
		for _, l := range page.Logs {
			if l.Status == "processing" {
				continue
			}
			if _, ok := t.seen[l.ID]; ok {
				continue
			}
			t.seen[l.ID] = l.Timestamp
			if l.Timestamp.After(newest) {
				newest = l.Timestamp
			}
			t.deliver(l)
		}
		if len(page.Logs) < MaxLogLimit {
			break
		}
	}
	if newest.IsZero() {
		return
	}
	t.cursor = newest
	floor := newest.Add(-t.lookback)
	for id, ts := range t.seen {
		if ts.Before(floor) {
			delete(t.seen, id)
		}
	}
}

// deliver hands l to every subscriber without blocking, dropping any whose buffer is full.
func (t *Tail) deliver(l Log) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for id, ch := range t.subs {
		select {
		case ch <- l:
		default:
			delete(t.subs, id)
			close(ch)
			t.log.Warn("dropped a request-log subscriber that fell behind")
		}
	}
}
