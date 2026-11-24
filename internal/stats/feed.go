package stats

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/47vigen/remora/internal/engine/bifrost"
)

const (
	feedPage    = 200
	feedCursor  = "request-log"
	feedBuffer  = 1024
	feedBacklog = 64 * 1024
	feedFlush   = 250 * time.Millisecond
)

// StartFeed subscribes before the tail starts. Cancel ctx before waiting for the feed to finish.
func StartFeed(ctx context.Context, s *Store, src bifrost.LogSource, tail *bifrost.Tail, pace time.Duration, rawDays int, log *slog.Logger) func() {
	rows, unsubscribe := tail.Subscribe(ctx, feedBuffer, feedBacklog)
	start := make(chan time.Time, 1)
	done := make(chan Cursor, 1)
	var wg sync.WaitGroup
	wg.Go(func() {
		select {
		case end := <-start:
			if pos, ok := backfill(ctx, s, src, end, pace, log); ok {
				done <- pos
			}
		case <-ctx.Done():
		}
	})
	wg.Go(func() { follow(ctx, s, tail, rows, unsubscribe, start, done, rawDays, log) })
	return wg.Wait
}

func follow(ctx context.Context, s *Store, tail *bifrost.Tail, rows <-chan bifrost.Log, unsubscribe func(), start chan<- time.Time, done <-chan Cursor, rawDays int, log *slog.Logger) {
	defer func() { unsubscribe() }()
	flush := time.NewTicker(feedFlush)
	defer flush.Stop()
	prune := time.NewTicker(time.Hour)
	defer prune.Stop()
	batch := make([]bifrost.Log, 0, feedPage)
	var pos Cursor
	ready, dropped := false, false
	for {
		select {
		case <-ctx.Done():
			return
		case pos = <-done:
			ready = true
		case now := <-prune.C:
			// IDs above the committed watermark may be read again on restart, even after a long outage.
			before := now.Add(-replayWindow)
			if pos.Timestamp.Before(before) {
				before = pos.Timestamp
			}
			if err := s.prune(ctx, now, rawDays, before); err != nil && ctx.Err() == nil {
				log.WarnContext(ctx, "pruning request stats failed", "err", err)
			}
		case row, ok := <-rows:
			if !ok {
				// shortcut: repair a dropped subscription on restart, add in-process replay if sustained overload is common.
				log.WarnContext(ctx, "stats log feed fell behind; restart to catch up")
				unsubscribe()
				rows, unsubscribe = tail.Subscribe(ctx, feedBuffer, feedBacklog)
				dropped = true
				continue
			}
			attributeKeyError(&row)
			batch = append(batch, row)
			if len(batch) < feedPage {
				continue
			}
		case <-flush.C:
		}
		first, settled := tail.Coverage(rows)
		if start != nil && !first.IsZero() {
			start <- first
			start = nil
		}
		// Don't make a new write every poll while idle. The replay window covers this extra minute.
		next := Cursor{Timestamp: settled.Truncate(time.Minute).Add(-time.Nanosecond)}
		if ready && !dropped && !settled.IsZero() && next.Timestamp.After(pos.Timestamp) {
			if !retryFeed(ctx, log, "checkpointing request stats failed", func() error {
				return s.IngestPage(ctx, batch, feedCursor, next)
			}) {
				return
			}
			pos = next
		} else if len(batch) > 0 && !retryFeed(ctx, log, "ingesting request stats failed", func() error {
			return s.Ingest(ctx, batch)
		}) {
			return
		}
		clear(batch)
		batch = batch[:0]
	}
}

// backfill bridges the persisted cursor to the start of the first live window, inclusively so seen handles the seam.
func backfill(ctx context.Context, s *Store, src bifrost.LogSource, end time.Time, pace time.Duration, log *slog.Logger) (Cursor, bool) {
	began := time.Now()
	var pos Cursor
	if !retryFeed(ctx, log, "reading request stats cursor failed", func() error {
		var err error
		pos, err = s.Cursor(ctx, feedCursor)
		return err
	}) {
		return Cursor{}, false
	}
	if pos.Timestamp.IsZero() {
		pos.Timestamp = began.UTC().AddDate(0, 0, -8)
	}
	start := pos.Timestamp
	var ties []bifrost.Log
	var total int
	lastProgress := began
	for !start.After(end) {
		var short bool
		if !retryFeed(ctx, log, "stats backfill page failed", func() error {
			from := start
			page, err := src.SearchLogs(ctx, bifrost.LogFilter{
				Start: &from, End: &end, Limit: feedPage, Offset: len(ties), SortBy: "timestamp", Order: "asc", NoContent: true,
			})
			if err != nil {
				return err
			}
			short = len(page.Logs) < feedPage
			rows := slices.Concat(ties, page.Logs)
			split := len(rows)
			if !short {
				// The list promises timestamp order, not ID order. Finish the last timestamp before checkpointing it.
				last := rows[split-1].Timestamp
				for split > 0 && rows[split-1].Timestamp.Equal(last) {
					split--
				}
			}
			next := pos
			batch := make([]bifrost.Log, 0, split)
			for _, row := range rows[:split] {
				if row.ID == "" || row.Timestamp.IsZero() {
					return errors.New("request stats row is missing its ID or timestamp")
				}
				if afterCursor(row, pos) {
					if afterCursor(row, next) {
						next = Cursor{row.Timestamp, row.ID}
					}
					attributeKeyError(&row)
					batch = append(batch, row)
				}
			}
			if short && end.After(next.Timestamp) {
				next = Cursor{Timestamp: end}
			}
			if err := s.IngestPage(ctx, batch, feedCursor, next); err != nil {
				return err
			}
			total += len(batch)
			pos = next
			ties = rows[split:]
			if len(ties) > 0 {
				start = ties[0].Timestamp
			}
			return nil
		}) {
			return Cursor{}, false
		}
		if short {
			break
		}
		if now := time.Now(); now.Sub(lastProgress) >= time.Minute {
			log.InfoContext(ctx, "stats backfill progress", "rows", total, "duration", now.Sub(began))
			lastProgress = now
		}
		if !waitFeed(ctx, pace) {
			return Cursor{}, false
		}
	}
	log.InfoContext(ctx, "stats backfill done", "rows", total, "duration", time.Since(began))
	return pos, true
}

func afterCursor(row bifrost.Log, pos Cursor) bool {
	return row.Timestamp.After(pos.Timestamp) || row.Timestamp.Equal(pos.Timestamp) && row.ID > pos.ID
}

func retryFeed(ctx context.Context, log *slog.Logger, message string, fn func() error) bool {
	var delay time.Duration
	for ctx.Err() == nil {
		if err := fn(); err == nil {
			return true
		} else if ctx.Err() == nil {
			next := min(max(2*delay, time.Second), time.Minute)
			if next != delay {
				log.WarnContext(ctx, message, "err", err, "retry_in", next)
			}
			delay = next
			if !waitFeed(ctx, delay) {
				break
			}
		}
	}
	return false
}

func waitFeed(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// attributeKeyError is the hook for attributing per-key errors once the list supplies failed key IDs.
func attributeKeyError(_ *bifrost.Log) {}
