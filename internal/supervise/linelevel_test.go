package supervise

import (
	"log/slog"
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
		`listening on 127.0.0.1:8081`:                                                                                 slog.LevelInfo,
	} {
		if got := lineLevel([]byte(line)); got != want {
			t.Errorf("lineLevel(%q) = %v, want %v", line, got, want)
		}
	}
}
