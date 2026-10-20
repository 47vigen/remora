package supervise

import (
	"bytes"
	"context"
	"log/slog"
	"slices"
	"testing"
)

func TestEngineLineLevel(t *testing.T) {
	for line, want := range map[string]slog.Level{
		`[2026-10-19 15:48:37] [--------] [info ] [gin_logger.go:108] 200 | 144ms | GET "/v8/management/credentials"`: slog.LevelDebug,
		`[2026-10-19 15:48:37] [--------] [debug] [auth.go:12] refresh`:                                               slog.LevelDebug,
		`[2026-10-19 15:48:37] [--------] [warn ] [auth.go:12] token expires soon`:                                    slog.LevelWarn,
		`[2026-10-19 15:48:37] [--------] [error] [auth.go:12] refresh failed`:                                        slog.LevelError,
		`{"level":"info","http.method":"GET","http.target":"/api/logs"}`:                                              slog.LevelDebug,
		`{"level":"warn","message":"provider slow"}`:                                                                  slog.LevelWarn,
		`{"level":"error","message":"provider down"}`:                                                                 slog.LevelError,
		`{"level":"fatal","message":"cannot bind"}`:                                                                   slog.LevelError,
		`[2026-10-19 15:48:37] [--------] [fatal] [main.go:9] cannot bind`:                                            slog.LevelError,
		`[2026-10-19 15:48:37] [--------] [panic] [main.go:9] nil map`:                                                slog.LevelError,
		`listening on 127.0.0.1:8081`:                                                                                 slog.LevelInfo,
	} {
		if got := lineLevel([]byte(line)); got != want {
			t.Errorf("lineLevel(%q) = %v, want %v", line, got, want)
		}
	}
}

type levels struct{ got []slog.Level }

func (h *levels) Enabled(context.Context, slog.Level) bool { return true }
func (h *levels) Handle(_ context.Context, r slog.Record) error {
	h.got = append(h.got, r.Level)
	return nil
}
func (h *levels) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *levels) WithGroup(string) slog.Handler      { return h }

func TestSplitRecordKeepsItsLevel(t *testing.T) {
	h := &levels{}
	l := &lineLogger{log: slog.New(h), stream: "stdout"}
	long := append([]byte(`[2026-10-19 15:48:37] [--------] [error] `), bytes.Repeat([]byte("x"), 2*maxLineLength)...)
	_, _ = l.Write(long)
	_, _ = l.Write([]byte("end of the long record\n"))
	_, _ = l.Write([]byte("plain line\n"))
	if n := len(h.got); n < 3 || slices.ContainsFunc(h.got[:n-1], func(v slog.Level) bool { return v != slog.LevelError }) ||
		h.got[n-1] != slog.LevelInfo {
		t.Errorf("levels = %v, want every chunk of the long record at ERROR, then INFO", h.got)
	}
}
