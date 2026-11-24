package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/47vigen/remora/internal/engine/bifrost"
)

type engineSearchFake struct {
	LogSource
	search func(context.Context, bifrost.LogFilter) (bifrost.LogPage, error)
}

func (f engineSearchFake) SearchLogs(ctx context.Context, filter bifrost.LogFilter) (bifrost.LogPage, error) {
	return f.search(ctx, filter)
}

func readLogs(ctx context.Context, h http.Handler, query string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/logs"+query, nil))
	return w
}

func TestEngineReadTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		src := engineSearchFake{search: func(ctx context.Context, _ bifrost.LogFilter) (bifrost.LogPage, error) {
			select {
			case <-ctx.Done():
				return bifrost.LogPage{}, ctx.Err()
			case <-time.After(30 * time.Second):
				return bifrost.LogPage{}, context.DeadlineExceeded
			}
		}}
		h := New(Deps{Logs: src, Names: &fakeNames{}}).Handler()
		start := time.Now()
		w := readLogs(t.Context(), h, "")
		if w.Code != http.StatusGatewayTimeout || !strings.Contains(w.Body.String(), `"code":"engine_timeout"`) ||
			!strings.Contains(w.Body.String(), "The request log took too long to answer; try a shorter range.") {
			t.Fatalf("response = %d %s", w.Code, w.Body)
		}
		if elapsed := time.Since(start); elapsed != 10*time.Second {
			t.Fatalf("elapsed = %s, want 10s", elapsed)
		}
	})
}

func TestEngineReadSingleFlight(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const readers = 8
		release := make(chan struct{})
		var calls atomic.Int32
		src := engineSearchFake{search: func(ctx context.Context, f bifrost.LogFilter) (bifrost.LogPage, error) {
			calls.Add(1)
			select {
			case <-ctx.Done():
				return bifrost.LogPage{}, ctx.Err()
			case <-release:
				return bifrost.LogPage{Total: int64(f.Offset + 1)}, nil
			}
		}}
		s := New(Deps{Logs: src, Names: &fakeNames{}})
		// Even identical URLs resolve to different timestamps when their default range moves.
		var ticks atomic.Int64
		s.now = func() time.Time { return time.Unix(0, ticks.Add(1)).UTC() }
		h := s.Handler()
		leader, cancel := context.WithCancel(t.Context())
		defer cancel()
		first := make(chan *httptest.ResponseRecorder, 1)
		go func() { first <- readLogs(leader, h, "") }()
		synctest.Wait()

		results := make(chan *httptest.ResponseRecorder, readers)
		for range readers {
			go func() { results <- readLogs(t.Context(), h, "") }()
		}
		otherPage := make(chan *httptest.ResponseRecorder, 1)
		go func() { otherPage <- readLogs(t.Context(), h, "?offset=1") }()
		synctest.Wait()
		cancel()
		synctest.Wait()
		if len(first) != 1 || len(results) != 0 {
			t.Error("canceling the leader did not release only its own request")
		}
		close(release)
		for range readers {
			if w := <-results; w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"total":1`) {
				t.Errorf("shared response = %d %s", w.Code, w.Body)
			}
		}
		if w := <-otherPage; w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"total":2`) {
			t.Errorf("distinct page = %d %s", w.Code, w.Body)
		}
		if n := calls.Load(); n != 2 {
			t.Errorf("engine reads = %d, want one for each page", n)
		}
	})
}

func TestEngineReadConcurrencyCap(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const readers = 8
		release := make(chan struct{})
		var calls atomic.Int32
		src := engineSearchFake{search: func(_ context.Context, _ bifrost.LogFilter) (bifrost.LogPage, error) {
			calls.Add(1)
			<-release // A slow engine must not keep the API callers waiting past their deadlines.
			return bifrost.LogPage{}, nil
		}}
		h := New(Deps{Logs: src, Names: &fakeNames{}}).Handler()
		results := make(chan *httptest.ResponseRecorder, readers)
		for i := range readers {
			go func() { results <- readLogs(t.Context(), h, "?offset="+strconv.Itoa(i)) }()
		}
		synctest.Wait()
		if n := calls.Load(); n != 4 {
			t.Errorf("active engine reads = %d, want 4", n)
		}
		time.Sleep(10 * time.Second)
		synctest.Wait()
		if n := len(results); n != readers {
			t.Errorf("completed requests at the deadline = %d, want %d", n, readers)
		}
		close(release)
		for range readers {
			if w := <-results; w.Code != http.StatusGatewayTimeout {
				t.Errorf("response = %d, want 504", w.Code)
			}
		}
		if n := calls.Load(); n != 4 {
			t.Errorf("engine reads after deadline = %d, want 4", n)
		}
	})
}
