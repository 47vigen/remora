package stats

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/47vigen/remora/internal/engine/bifrost"
)

// feedLogs mimics the list's inclusive bounds and offset, over a fixed timestamp-ordered fixture.
type feedLogs struct {
	mu    sync.Mutex
	rows  []bifrost.Log
	reads []feedRead
}

type feedRead struct {
	at time.Time
	bifrost.LogFilter
}

func (f *feedLogs) SearchLogs(_ context.Context, q bifrost.LogFilter) (bifrost.LogPage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads = append(f.reads, feedRead{time.Now(), q})
	var matched []bifrost.Log
	for _, row := range f.rows {
		if (q.Start == nil || !row.Timestamp.Before(*q.Start)) && (q.End == nil || !row.Timestamp.After(*q.End)) {
			matched = append(matched, row)
		}
	}
	lo := min(q.Offset, len(matched))
	return bifrost.LogPage{Logs: matched[lo:min(lo+q.Limit, len(matched))], Total: int64(len(matched))}, nil
}

func (*feedLogs) GetLog(context.Context, string) (bifrost.Log, error) {
	return bifrost.Log{}, bifrost.ErrNotFound
}

func feedRow(id string, at time.Time) bifrost.Log {
	return bifrost.Log{ID: id, Timestamp: at, Status: "success", Provider: "p", Model: "m"}
}

func TestFeedResumesFromCursor(t *testing.T) {
	s := openTestStore(t, filepath.Join(t.TempDir(), "stats.db"))
	at := time.Now().UTC().Truncate(time.Minute).Add(-time.Hour)
	end := at.Add(time.Minute)
	// The engine's timestamp sort need not break ties by ID. More than one page can share a timestamp.
	var rows []bifrost.Log
	for i := 205; i >= 0; i-- {
		rows = append(rows, feedRow(fmt.Sprintf("%03d", i), at))
	}
	ctx := t.Context()
	must(t, s.IngestPage(ctx, rows[4:], feedCursor, Cursor{at, "201"}))
	must(t, s.Prune(ctx, time.Now().Add(time.Hour), 7)) // resume must not rely on replay IDs
	src := &feedLogs{rows: rows}
	backfill(ctx, s, src, end, time.Nanosecond, slog.New(slog.DiscardHandler))
	got, err := s.Query(ctx, at, end, 0, nil)
	must(t, err)
	if got.Summary.Requests != 206 {
		t.Fatalf("requests = %d, want 206 after resuming tied timestamps", got.Summary.Requests)
	}
	if len(src.reads) != 2 || !src.reads[0].Start.Equal(at) || src.reads[1].Offset != 200 {
		t.Fatalf("want two pages from the cursor's inclusive timestamp, got %+v", src.reads)
	}
}

func TestFeedBackfillPaced(t *testing.T) {
	s := openTestStore(t, filepath.Join(t.TempDir(), "stats.db"))
	synctest.Test(t, func(t *testing.T) {
		end := time.Now().Add(-10 * time.Minute)
		src := &feedLogs{}
		for i := range 405 {
			src.rows = append(src.rows, feedRow(fmt.Sprint(i), end.Add(time.Duration(i-405)*time.Second)))
		}
		backfill(t.Context(), s, src, end, 500*time.Millisecond, slog.New(slog.DiscardHandler))
		if len(src.reads) != 3 {
			t.Fatalf("list reads = %d, want 3", len(src.reads))
		}
		for i, read := range src.reads {
			if read.Limit != 200 || !read.NoContent || read.Order != "asc" || read.SortBy != "timestamp" || !read.End.Equal(end) {
				t.Fatalf("backfill read %d: %+v", i, read.LogFilter)
			}
			if i != 0 && read.at.Sub(src.reads[i-1].at) < 500*time.Millisecond {
				t.Fatalf("backfill page %d was not paced: %v", i, read.at.Sub(src.reads[i-1].at))
			}
		}
		if want := end.Add(10*time.Minute - 8*24*time.Hour); !src.reads[0].Start.Equal(want) {
			t.Fatalf("empty store starts at %v, want %v", src.reads[0].Start, want)
		}
	})
}

func TestFeedCheckpointsLiveAfterBackfill(t *testing.T) {
	s := openTestStore(t, filepath.Join(t.TempDir(), "stats.db"))
	synctest.Test(t, func(t *testing.T) {
		at := time.Now().Add(-time.Second)
		src := &feedLogs{rows: []bifrost.Log{feedRow("live", at)}}
		log := slog.New(slog.DiscardHandler)
		run := func(forTime time.Duration) {
			tail := bifrost.NewTail(src, time.Minute, nil, log)
			ctx, cancel := context.WithCancel(t.Context())
			wait := StartFeed(ctx, s, src, tail, time.Second, 7, log)
			go tail.Run(ctx)
			time.Sleep(forTime)
			cancel()
			wait()
		}
		run(20 * time.Minute)
		// Simulate a restart after the replay IDs have expired. Backfill must not reread a settled live row.
		must(t, s.Prune(t.Context(), time.Now(), 7))
		run(2 * time.Minute)
		got, err := s.Query(t.Context(), at, at, 0, nil)
		must(t, err)
		if got.Summary.Requests != 1 {
			t.Fatalf("settled live row counted %d times, want 1", got.Summary.Requests)
		}
	})
}

func TestFeedDoesNotDoubleCount(t *testing.T) {
	s := openTestStore(t, filepath.Join(t.TempDir(), "stats.db"))
	synctest.Test(t, func(t *testing.T) {
		now := time.Now()
		boundary := now.Add(time.Second - 10*time.Minute)
		src := &feedLogs{rows: []bifrost.Log{
			feedRow("historical", boundary.Add(-time.Second)),
			feedRow("overlap", boundary),
			feedRow("latest", now),
		}}
		log := slog.New(slog.DiscardHandler)
		tail := bifrost.NewTail(src, time.Second, nil, log)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		wait := StartFeed(ctx, s, src, tail, time.Second, 7, log)
		defer func() { cancel(); wait() }()
		go tail.Run(ctx)
		time.Sleep(2 * time.Second)
		synctest.Wait()
		got, err := s.Query(ctx, boundary.Add(-time.Second), now, 0, nil)
		must(t, err)
		if got.Summary.Requests != 3 {
			t.Fatalf("requests = %d, want one historical + one overlapping + one live", got.Summary.Requests)
		}
		backfillReads := 0
		for _, read := range src.reads {
			if read.End != nil {
				backfillReads++
				if !read.End.Equal(boundary) {
					t.Fatalf("backfill end = %v, want first tail window at %v", read.End, boundary)
				}
			}
		}
		if backfillReads != 1 {
			t.Fatalf("backfill reads = %d, want 1", backfillReads)
		}
	})
}
