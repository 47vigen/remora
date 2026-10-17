package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/47vigen/remora/internal/config"
)

// TestMain turns the test binary into a fake engine when it runs under an engine's name, so serve can start its
// engines without the real binaries.
func TestMain(m *testing.M) {
	switch filepath.Base(os.Args[0]) {
	case "bifrost", "cliproxyapi":
		os.Exit(runFakeEngine(os.Args[1:]))
	}
	os.Exit(m.Run())
}

var configPort = regexp.MustCompile(`(?m)^\s*port:\s*(\d+)`)

// runFakeEngine answers 200 to every request on the engine's port until SIGTERM.
func runFakeEngine(args []string) int {
	terms := make(chan os.Signal, 1)
	signal.Notify(terms, syscall.SIGTERM)
	var port string
	for i := 0; i+1 < len(args); i++ {
		switch args[i] {
		case "-port":
			port = args[i+1]
		case "-config":
			b, err := os.ReadFile(args[i+1]) //nolint:gosec // the config path serve passes
			if m := configPort.FindSubmatch(b); err == nil && m != nil {
				port = string(m[1])
			}
		}
	}
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", port)) //nolint:noctx // fake engine process
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "fake engine:", err)
		return 2
	}
	go func() {
		_ = http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { //nolint:gosec // test fake
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, "{}")
		}))
	}()
	<-terms
	return 0
}

// fakeEngineDir links the test binary in as both engines and writes the versions.json that vouches for it.
func fakeEngineDir(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(exe) //nolint:gosec // the test binary itself
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	dir := t.TempDir()
	for _, name := range []string{"bifrost", "cliproxyapi"} {
		if err := os.Symlink(exe, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	v := fmt.Sprintf(`{"bifrost":{"version":"test","sha256":%q},"cliproxyapi":{"version":"test","sha256":%q}}`,
		hex.EncodeToString(sum[:]), hex.EncodeToString(sum[:]))
	if err := os.WriteFile(filepath.Join(dir, "versions.json"), []byte(v), 0o600); err != nil { //nolint:gosec // a test temp dir
		t.Fatal(err)
	}
	return dir
}

// TestServe_StopsWithinGrace is the upgrade path: SIGTERM arrives while a request is still in flight (a stream
// that would outlive Docker's grace period), and serve must still drain, stop the engines and return nil before
// stopTimeout, which the compose example's stop_grace_period covers.
func TestServe_StopsWithinGrace(t *testing.T) {
	if testing.Short() {
		t.Skip("starts fake engines and waits out the drain")
	}
	listen := "127.0.0.1:" + strconv.Itoa(freePort(t))
	env := map[string]string{
		config.EnvListen:              listen,
		config.EnvData:                t.TempDir(),
		config.EnvAdminPassword:       "test-admin-password",
		config.EnvEngineDir:           fakeEngineDir(t),
		config.EnvPortRouter:          strconv.Itoa(freePort(t)),
		config.EnvPortAccounts:        strconv.Itoa(freePort(t)),
		config.EnvPortSpeech:          strconv.Itoa(freePort(t)),
		config.EnvSonioxBaseURL:       "http://127.0.0.1:1",
		config.EnvSonioxSweepInterval: "1h",
		config.EnvQuotaInterval:       "1h",
	}
	cfg, err := config.Load(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}

	ctx, signal := context.WithCancel(context.Background())
	defer signal()
	served := make(chan error, 1)
	go func() { served <- serve(ctx, cfg, slog.New(slog.DiscardHandler)) }()

	deadline := time.Now().Add(30 * time.Second)
	for {
		resp, err := http.Get("http://" + listen + "/healthz") //nolint:noctx // polling a local listener
		if err == nil {
			_ = resp.Body.Close()
			break
		}
		select {
		case err := <-served:
			t.Fatalf("serve returned before listening: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("serve never listened")
		}
		time.Sleep(50 * time.Millisecond)
	}

	// An in-flight request that never finishes on its own: its body promises more than it sends.
	conn, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", listen)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_, _ = io.WriteString(conn, "POST /api/auth/login HTTP/1.1\r\nHost: x\r\nContent-Type: application/json\r\n"+
		"Content-Length: 1000\r\n\r\n{\"password\":")
	time.Sleep(200 * time.Millisecond)

	start := time.Now()
	signal()
	select {
	case err := <-served:
		if err != nil {
			t.Errorf("serve = %v, want nil after the signal", err)
		}
		if took := time.Since(start); took > stopTimeout {
			t.Errorf("serve took %v after the signal, want under %v", took, stopTimeout)
		}
	case <-time.After(stopTimeout + 5*time.Second):
		t.Fatalf("serve still running %v after the signal", stopTimeout+5*time.Second)
	}
}
