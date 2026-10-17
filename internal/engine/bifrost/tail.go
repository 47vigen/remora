package bifrost

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

const (
	defaultTailInterval = time.Second
	// defaultLookback is how far before the newest row seen each poll reaches back. A row is written when its request
	// completes but carries the request's start time, so a long request lands behind rows already seen.
	defaultLookback = 10 * time.Minute
	// maxRunning bounds the requests still in processing that a Tail follows past the lookback.
	maxRunning = 10_000
)

// LogSource is what a Tail polls; *Client satisfies it.
type LogSource interface {
	SearchLogs(ctx context.Context, f LogFilter) (LogPage, error)
	GetLog(ctx context.Context, id string) (Log, error)
}

// Tail follows the front door's request log by polling it, since the engine pushes no log rows. It delivers each
// completed row once to every subscriber, polls only while someone is subscribed, and never blocks on a subscriber:
// rows that do not fit a subscriber's channel wait in its backlog and are handed over on later polls.
type Tail struct {
	src      LogSource
	interval time.Duration
	lookback time.Duration
	status   []string // the row statuses each poll asks for; nil = every row
	log      *slog.Logger
	now      func() time.Time

	mu     sync.Mutex
	subs   map[int]*subscriber
	nextID int

	// Poll state, touched only by the goroutine that polls.
	cursor  time.Time            // when the last fully read window began, or the newest row it delivered if later
	seen    map[string]time.Time // delivered row id → its timestamp
	running map[string]time.Time // id of a row seen in processing → its timestamp, followed until it completes
}

// subscriber is one subscription: its channel and up to backlog rows that did not fit it yet.
type subscriber struct {
	ch      chan Log
	pending []Log
	backlog int
}

// flush moves pending rows into the channel while it has room.
func (s *subscriber) flush() {
	for len(s.pending) > 0 {
		select {
		case s.ch <- s.pending[0]:
			s.pending = s.pending[1:]
		default:
			return
		}
	}
	s.pending = nil
}

// NewTail returns a Tail over src that polls every interval (1s when interval ≤ 0) for rows of the given statuses
// (nil = every row). A nil log discards.
func NewTail(src LogSource, interval time.Duration, status []string, log *slog.Logger) *Tail {
	if interval <= 0 {
		interval = defaultTailInterval
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Tail{
		src: src, interval: interval, lookback: defaultLookback, status: status, log: log, now: time.Now,
		subs: map[int]*subscriber{}, seen: map[string]time.Time{}, running: map[string]time.Time{},
	}
}

// Subscribe returns a channel of completed rows with room for buffer rows, and a function that ends the
// subscription. Up to backlog more rows wait in the tail and are handed over on later polls, so a subscriber that
// falls behind loses nothing; one that falls further behind is dropped: its channel is closed. The cancel function is
// safe to call more than once and after a drop.
func (t *Tail) Subscribe(buffer, backlog int) (<-chan Log, func()) {
	if buffer < 1 {
		buffer = 1
	}
	ch := make(chan Log, buffer)
	t.mu.Lock()
	id := t.nextID
	t.nextID++
	t.subs[id] = &subscriber{ch: ch, backlog: backlog}
	t.mu.Unlock()
	return ch, func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		if s, ok := t.subs[id]; ok {
			delete(t.subs, id)
			close(s.ch)
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

// poll hands subscribers their backlog, then reads the whole window from cursor − lookback, page by page, and delivers
// the rows not seen yet. The cursor advances only once every page of the window has been read, so a burst larger than
// one page is never skipped; a failed page leaves the cursor where it was and the next poll reads the window again.
// It advances to the poll's own start even when no row arrived: with a status filter the matching rows can be days
// apart, and a cursor held at the last of them would make every poll read every row since. Rows seen in processing
// that fall behind the window are fetched one by one until they complete.
func (t *Tail) poll(ctx context.Context) {
	t.deliver()
	began := t.now()
	from := t.cursor
	if from.IsZero() {
		from = began
	}
	start := from.Add(-t.lookback)
	newest := began
	if t.cursor.After(newest) {
		newest = t.cursor
	}
	for offset := 0; ; offset += MaxLogLimit {
		page, err := t.src.SearchLogs(ctx, LogFilter{
			Start: &start, Status: t.status, Limit: MaxLogLimit, Offset: offset, SortBy: "timestamp", Order: "asc",
		})
		if err != nil {
			if ctx.Err() == nil {
				t.log.WarnContext(ctx, "reading the request log failed", "err", err)
			}
			return
		}
		for _, l := range page.Logs {
			if l.Status == "processing" {
				t.follow(l)
				continue
			}
			delete(t.running, l.ID)
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
	t.recheck(ctx, start)
	t.cursor = newest
	floor := newest.Add(-t.lookback)
	for id, ts := range t.seen {
		if ts.Before(floor) {
			delete(t.seen, id)
		}
	}
}

// follow remembers a row in processing, forgetting the oldest one when maxRunning are followed already.
func (t *Tail) follow(l Log) {
	if _, ok := t.running[l.ID]; !ok && len(t.running) >= maxRunning {
		oldest := ""
		for id, ts := range t.running {
			if oldest == "" || ts.Before(t.running[oldest]) {
				oldest = id
			}
		}
		delete(t.running, oldest)
	}
	t.running[l.ID] = l.Timestamp
}

// recheck fetches each followed row older than start, which the window no longer reads, and delivers it once it has
// completed. A row the engine no longer has is forgotten.
func (t *Tail) recheck(ctx context.Context, start time.Time) {
	for id, ts := range t.running {
		if !ts.Before(start) {
			continue
		}
		l, err := t.src.GetLog(ctx, id)
		switch {
		case errors.Is(err, ErrNotFound):
			delete(t.running, id)
		case err != nil:
			if ctx.Err() == nil {
				t.log.WarnContext(ctx, "reading a request-log row failed", "id", id, "err", err)
			}
			return
		case l.Status != "processing":
			delete(t.running, id)
			t.seen[id] = l.Timestamp
			t.deliver(l)
		}
	}
}

// deliver queues ls for every subscriber and hands each as many waiting rows as its channel has room for, without
// blocking. A subscriber whose waiting rows outgrow its backlog is dropped.
func (t *Tail) deliver(ls ...Log) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for id, s := range t.subs {
		s.pending = append(s.pending, ls...)
		s.flush()
		if len(s.pending) > s.backlog {
			delete(t.subs, id)
			close(s.ch)
			t.log.Warn("dropped a request-log subscriber that fell behind")
		}
	}
}
