package soniox

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	fake "github.com/47vigen/remora/test/fakes/soniox"
)

var t0 = time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)

func newSweeper(t *testing.T, h http.Handler, keys ...string) *Sweeper {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &Sweeper{
		Client: NewClient(srv.URL, srv.Client()),
		Keys:   func(context.Context) ([]string, error) { return keys, nil },
		MaxAge: time.Hour,
		Now:    func() time.Time { return t0 },
		Logger: slog.New(slog.DiscardHandler),
	}
}

func liveRefs(f *fake.Fake) map[string]bool {
	out := map[string]bool{}
	for _, o := range f.State().Live {
		out[o.Kind+" "+o.ClientReferenceID] = true
	}
	return out
}

func TestSweepDeletesOldOwned(t *testing.T) {
	f := fake.New()
	f.Seed(fake.Object{ID: "fa", Kind: "file", Key: "k", ClientReferenceID: "remora:a", CreatedAt: t0.Add(-2 * time.Hour)})
	f.Seed(fake.Object{ID: "ta", Kind: "transcription", Key: "k", ClientReferenceID: "remora:a", Status: "completed", FileID: "fa", CreatedAt: t0.Add(-2 * time.Hour)})
	f.Seed(fake.Object{Kind: "transcription", Key: "k", ClientReferenceID: "remora:e", Status: "error", CreatedAt: t0.Add(-2 * time.Hour)})
	res, err := newSweeper(t, f, "k").SweepOnce(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if res != (SweepResult{Transcriptions: 2, Files: 1}) {
		t.Fatalf("res = %+v", res)
	}
	if st := f.State(); len(st.Live) != 0 || len(st.Deleted) != 3 {
		t.Fatalf("state %+v", st)
	}
}

func TestSweepKeepsYoungAndForeign(t *testing.T) {
	f := fake.New()
	f.Seed(fake.Object{Kind: "file", Key: "k", ClientReferenceID: "remora:b", CreatedAt: t0.Add(-30 * time.Minute)})
	f.Seed(fake.Object{Kind: "transcription", Key: "k", ClientReferenceID: "remora:b", Status: "completed", CreatedAt: t0.Add(-30 * time.Minute)})
	f.Seed(fake.Object{Kind: "file", Key: "k", ClientReferenceID: "app:x", CreatedAt: t0.Add(-5 * time.Hour)})
	f.Seed(fake.Object{Kind: "transcription", Key: "k", ClientReferenceID: "app:x", Status: "completed", CreatedAt: t0.Add(-5 * time.Hour)})
	f.Seed(fake.Object{Kind: "file", Key: "k", CreatedAt: t0.Add(-5 * time.Hour)})
	res, err := newSweeper(t, f, "k").SweepOnce(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if res != (SweepResult{}) || len(f.State().Live) != 5 {
		t.Fatalf("res %+v, live %v", res, liveRefs(f))
	}
}

func TestSweepKeepsYoungActiveJobAndItsFile(t *testing.T) {
	f := fake.New()
	f.Seed(fake.Object{ID: "fc", Kind: "file", Key: "k", ClientReferenceID: "remora:c", CreatedAt: t0.Add(-3 * time.Hour)})
	f.Seed(fake.Object{Kind: "transcription", Key: "k", ClientReferenceID: "remora:c", Status: "processing", FileID: "fc", CreatedAt: t0.Add(-30 * time.Minute)})
	f.Seed(fake.Object{ID: "fx", Kind: "file", Key: "k", ClientReferenceID: "remora:x", CreatedAt: t0.Add(-3 * time.Hour)})
	f.Seed(fake.Object{Kind: "transcription", Key: "k", ClientReferenceID: "app:x", Status: "queued", FileID: "fx", CreatedAt: t0.Add(-3 * time.Hour)})
	res, err := newSweeper(t, f, "k").SweepOnce(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if st := f.State(); res != (SweepResult{}) || len(st.Live) != 4 {
		t.Fatalf("res %+v, state %+v", res, st)
	}
}

func TestSweepExpiresAbandonedJob(t *testing.T) {
	f := fake.New()
	f.Seed(fake.Object{ID: "fc", Kind: "file", Key: "k", ClientReferenceID: "remora:c", CreatedAt: t0.Add(-3 * time.Hour)})
	f.Seed(fake.Object{Kind: "transcription", Key: "k", ClientReferenceID: "remora:c", Status: "processing", FileID: "fc", CreatedAt: t0.Add(-3 * time.Hour)})
	f.Seed(fake.Object{ID: "fq", Kind: "file", Key: "k", ClientReferenceID: "remora:q", CreatedAt: t0.Add(-3 * time.Hour)})
	f.Seed(fake.Object{Kind: "transcription", Key: "k", ClientReferenceID: "remora:q", Status: "queued", FileID: "fq", CreatedAt: t0.Add(-3 * time.Hour)})
	res, err := newSweeper(t, f, "k").SweepOnce(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	// Both files go, which makes the jobs fail; the jobs themselves still run
	// and wait for the next sweep.
	st := f.State()
	if res != (SweepResult{Files: 2}) || len(st.Live) != 2 {
		t.Fatalf("res %+v, state %+v", res, st)
	}
	for _, o := range st.Live {
		if o.Kind != "transcription" {
			t.Fatalf("file of an abandoned job kept: %+v", o)
		}
	}
}

func TestSweepEachKeyIndependently(t *testing.T) {
	f := fake.New()
	f.Seed(fake.Object{Kind: "file", Key: "k", ClientReferenceID: "remora:a", CreatedAt: t0.Add(-2 * time.Hour)})
	f.Seed(fake.Object{Kind: "transcription", Key: "k", ClientReferenceID: "remora:a", Status: "completed", CreatedAt: t0.Add(-2 * time.Hour)})
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer bad" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"status_code":401,"error_type":"unauthenticated","message":"Invalid API key."}`)
			return
		}
		f.ServeHTTP(w, r)
	})
	res, err := newSweeper(t, h, "bad", "k").SweepOnce(t.Context())
	if err == nil {
		t.Fatal("want an error for the bad key")
	}
	if res != (SweepResult{Transcriptions: 1, Files: 1}) || len(f.State().Live) != 0 {
		t.Fatalf("res %+v, live %v", res, liveRefs(f))
	}
}

func TestSweepKeySourceError(t *testing.T) {
	s := newSweeper(t, fake.New())
	s.Keys = func(context.Context) ([]string, error) { return nil, io.ErrUnexpectedEOF }
	if _, err := s.SweepOnce(t.Context()); err == nil {
		t.Fatal("want the key source error")
	}
}

func TestSweepRunStopsOnCancel(t *testing.T) {
	s := newSweeper(t, fake.New())
	var sweeps atomic.Int32
	s.Keys = func(context.Context) ([]string, error) {
		sweeps.Add(1)
		return []string{"k"}, nil
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.Run(ctx, 10*time.Millisecond)
	}()
	time.Sleep(35 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Run did not return within 100ms of the cancel")
	}
	if sweeps.Load() < 1 {
		t.Fatal("no sweep happened")
	}
}
