package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"
)

func TestPasswordLogLevelIsNeverFiltered(t *testing.T) {
	for _, lvl := range []slog.Level{slog.LevelDebug, slog.LevelInfo, slog.LevelWarn, slog.LevelError} {
		logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: lvl}))
		got := passwordLogLevel(t.Context(), logger)
		if !logger.Enabled(t.Context(), got) {
			t.Errorf("configured %v: password logged at %v, which is filtered out", lvl, got)
		}
		if got < slog.LevelWarn {
			t.Errorf("configured %v: password logged at %v, want warn or higher", lvl, got)
		}
	}
}

// TestNewServer_NoWriteTimeout guards /v1 streams: a write or read timeout would cut a long answer or upload.
func TestNewServer_NoWriteTimeout(t *testing.T) {
	srv := newServer(context.Background(), ":0", http.NewServeMux())
	if srv.WriteTimeout != 0 || srv.ReadTimeout != 0 {
		t.Errorf("WriteTimeout = %v, ReadTimeout = %v, want 0", srv.WriteTimeout, srv.ReadTimeout)
	}
	if srv.ReadHeaderTimeout != readHeaderTimeout {
		t.Errorf("ReadHeaderTimeout = %v, want %v", srv.ReadHeaderTimeout, readHeaderTimeout)
	}
}

func TestServeLoggerScrubs(t *testing.T) {
	var b bytes.Buffer
	newServeLogger(&b, slog.LevelInfo).Error("reconcile: sync", "err", errors.New("bifrost: status 500 (key sk-bf-abc)"))
	if out := b.String(); strings.Contains(strings.ToLower(out), "bifrost") || strings.Contains(out, "sk-bf-") {
		t.Fatalf("log = %s, want engine words and keys scrubbed", out)
	}
}
