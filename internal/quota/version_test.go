package quota

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestVersionReadsManifestAtMostEvery3h(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte("version: 2.30.1\nfiles: []\n"))
	}))
	t.Cleanup(srv.Close)
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	v := newVersionCache(srv.Client(), srv.URL)
	v.now = func() time.Time { return now }
	for range 10 {
		if got := v.userAgent(context.Background()); got != "antigravity/hub/2.30.1 darwin/arm64" {
			t.Fatalf("userAgent = %q", got)
		}
		now = now.Add(17 * time.Minute)
	}
	if hits.Load() != 1 {
		t.Errorf("manifest reads in under 3h = %d, want 1", hits.Load())
	}
	now = now.Add(time.Hour)
	v.userAgent(context.Background())
	if hits.Load() != 2 {
		t.Errorf("manifest reads after 3h = %d, want 2", hits.Load())
	}
}

func TestVersionFallsBackToFloor(t *testing.T) {
	var fail atomic.Bool
	fail.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if fail.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte("version: 2.31.0\n"))
	}))
	t.Cleanup(srv.Close)
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	v := newVersionCache(srv.Client(), srv.URL)
	v.now = func() time.Time { return now }
	if got := v.userAgent(context.Background()); got != agFloorAgent {
		t.Errorf("first read failing: %q, want the floor", got)
	}
	fail.Store(false)
	now = now.Add(antigravityVersionTTL)
	if got := v.userAgent(context.Background()); got != "antigravity/hub/2.31.0 darwin/arm64" {
		t.Errorf("after a good read: %q", got)
	}
	fail.Store(true)
	now = now.Add(antigravityVersionTTL)
	if got := v.userAgent(context.Background()); got != "antigravity/hub/2.31.0 darwin/arm64" {
		t.Errorf("a later failure: %q, want the last good version", got)
	}
}
