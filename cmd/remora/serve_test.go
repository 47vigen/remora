package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/47vigen/remora/internal/config"
	"github.com/47vigen/remora/internal/store"
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

func TestNewReconciler_SpeechAdapter(t *testing.T) {
	cfg, err := config.Load(func(k string) string {
		return map[string]string{"REMORA_PORT_SPEECH": "19000", "REMORA_SONIOX_DEADLINE": "1h"}[k]
	})
	if err != nil {
		t.Fatal(err)
	}
	r := newReconciler(cfg, nil, nil, slog.New(slog.DiscardHandler))
	if r.SonioxURL != "http://127.0.0.1:19000" || r.SonioxDeadline != time.Hour {
		t.Errorf("SonioxURL = %q, SonioxDeadline = %v", r.SonioxURL, r.SonioxDeadline)
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	return ln.Addr().(*net.TCPAddr).Port
}

// TestStartSpeech serves the adapter on its loopback port, answers in the OpenAI error shape, and stops cleanly.
func TestStartSpeech(t *testing.T) {
	port := strconv.Itoa(freePort(t))
	cfg, err := config.Load(func(k string) string {
		return map[string]string{
			"REMORA_PORT_SPEECH":           port,
			"REMORA_SONIOX_BASE_URL":       "http://127.0.0.1:1",
			"REMORA_SONIOX_SWEEP_INTERVAL": "1h",
		}[k]
	})
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "remora.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	stop, err := startSpeech(t.Context(), cfg, st, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("startSpeech: %v", err)
	}
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://"+cfg.SonioxAddr+"/v1/audio/transcriptions", strings.NewReader("x"))
	req.Header.Set("Authorization", "Bearer test-key")
	req.Header.Set("Content-Type", "text/plain")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("adapter not reachable: %v", err)
	}
	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest || body.Error.Message == "" {
		t.Errorf("status %d, error %q; want 400 with an OpenAI error", resp.StatusCode, body.Error.Message)
	}

	done := make(chan struct{})
	go func() { stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("stop did not return")
	}
	if c, err := (&net.Dialer{Timeout: time.Second}).DialContext(t.Context(), "tcp", cfg.SonioxAddr); err == nil {
		_ = c.Close()
		t.Error("adapter still listening after stop")
	}
}
