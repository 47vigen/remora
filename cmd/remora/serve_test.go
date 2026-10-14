package main

import (
	"bytes"
	"log/slog"
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
