package supervise

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Environment variables that turn the test binary into a fake engine.
const (
	helperModeEnv    = "REMORA_HELPER_MODE"
	helperNameEnv    = "REMORA_HELPER_NAME"
	helperPortEnv    = "REMORA_HELPER_PORT"
	helperDirEnv     = "REMORA_HELPER_DIR"
	helperCrashMSEnv = "REMORA_HELPER_CRASH_MS"
)

func TestMain(m *testing.M) {
	if mode := os.Getenv(helperModeEnv); mode != "" {
		os.Exit(runHelper(mode))
	}
	os.Exit(m.Run())
}

// runHelper acts as a fake engine. It never returns while it is healthy; it exits on SIGTERM (except in
// ignore-term mode) or, in crash-after mode, after the configured delay on its first run.
func runHelper(mode string) int {
	name := os.Getenv(helperNameEnv)
	dir := os.Getenv(helperDirEnv)
	addr := net.JoinHostPort("127.0.0.1", os.Getenv(helperPortEnv))

	terms := make(chan os.Signal, 1)
	signal.Notify(terms, syscall.SIGTERM)

	status := http.StatusOK
	if strings.HasPrefix(mode, "never-healthy") {
		status = http.StatusInternalServerError
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
	})
	ln, err := net.Listen("tcp", addr) //nolint:noctx // test helper process
	if err != nil {
		fmt.Fprintln(os.Stderr, "helper listen:", err)
		return 2
	}
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: time.Second}
	go func() { _ = srv.Serve(ln) }()
	fmt.Printf("helper %s listening on %s\nsecond line\n", name, addr)
	fmt.Fprintf(os.Stderr, "helper %s mode %s\n", name, mode)

	var crash <-chan time.Time
	if mode == "crash-after" {
		marker := filepath.Join(dir, name+".crashed")                  // #nosec G703 -- dir comes from the test that spawned us
		if _, err := os.Stat(marker); errors.Is(err, fs.ErrNotExist) { // #nosec G703 -- test-owned temp dir
			if err := os.WriteFile(marker, nil, 0o600); err != nil { // #nosec G703 -- test-owned temp dir
				fmt.Fprintln(os.Stderr, "helper marker:", err)
				return 2
			}
			ms, _ := strconv.Atoi(os.Getenv(helperCrashMSEnv))
			crash = time.After(time.Duration(ms) * time.Millisecond)
		}
	}

	for {
		select {
		case <-crash:
			fmt.Fprintln(os.Stderr, "helper crashing")
			return 1
		case <-terms:
			if err := appendStop(dir, name); err != nil {
				fmt.Fprintln(os.Stderr, "helper stops:", err)
			}
			if !strings.HasSuffix(mode, "ignore-term") {
				return 0
			}
		}
	}
}

func appendStop(dir, name string) error {
	f, err := os.OpenFile(filepath.Join(dir, "stops"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) // #nosec G304 G703 -- test-owned temp dir
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(f, "%s %d\n", name, time.Now().UnixNano()); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// helperEngine returns an Engine that runs this test binary as a fake engine in the given mode, and the versions
// map that matches it. The helper's directory is its own temporary directory, see helperDir.
func helperEngine(t *testing.T, name, mode string) (Engine, map[string]EngineVersion) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("executable: %v", err)
	}
	port := freePort(t)
	e := Engine{
		Name: name,
		Bin:  exe,
		Env: []string{
			helperModeEnv + "=" + mode,
			helperNameEnv + "=" + name,
			helperPortEnv + "=" + strconv.Itoa(port),
			helperDirEnv + "=" + t.TempDir(),
			helperCrashMSEnv + "=200",
		},
		HealthURL: fmt.Sprintf("http://127.0.0.1:%d/health", port),
	}
	return e, map[string]EngineVersion{name: {Version: name + "-v1", SHA256: fileSHA256(t, exe)}}
}

// helperDir returns the directory a helper engine writes its stops file to.
func helperDir(e Engine) string {
	for _, kv := range e.Env {
		if v, ok := strings.CutPrefix(kv, helperDirEnv+"="); ok {
			return v
		}
	}
	return ""
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0") //nolint:noctx // test helper
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()
	return ln.Addr().(*net.TCPAddr).Port
}

func fileSHA256(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path) // #nosec G304 -- test reads its own binary
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		t.Fatalf("hash: %v", err)
	}
	return hex.EncodeToString(h.Sum(nil))
}
