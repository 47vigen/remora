//go:build unix

package supervise

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func testLogger(t *testing.T) *slog.Logger {
	return slog.New(slog.NewTextHandler(t.Output(), &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// stopAtCleanup stops the supervisor when the test ends so no helper process outlives it.
func stopAtCleanup(t *testing.T, s *Supervisor) {
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := s.Stop(ctx); err != nil {
			t.Errorf("Stop: %v", err)
		}
	})
}

// startAsync runs Start in a goroutine and returns the pid of the first child once it is launched.
func startAsync(ctx context.Context, t *testing.T, s *Supervisor, engines ...Engine) (int, <-chan error) {
	t.Helper()
	errc := make(chan error, 1)
	go func() { errc <- s.Start(ctx, engines...) }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if pids := s.pids(); len(pids) > 0 {
			return pids[0], errc
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no child process was started")
	return 0, nil
}

func assertGone(t *testing.T, pid int) {
	t.Helper()
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("pid %d still exists: kill(0) = %v", pid, err)
	}
}

func TestStartWaitsForHealth(t *testing.T) {
	e, versions := helperEngine(t, "bifrost", "healthy")
	s := New(testLogger(t), versions, Options{HealthTimeout: 10 * time.Second, HealthInterval: 20 * time.Millisecond})
	stopAtCleanup(t, s)

	if err := s.Start(t.Context(), e); err != nil {
		t.Fatalf("Start: %v", err)
	}
	want := []EngineStatus{{Name: "bifrost", Version: "bifrost-v1", Up: true, Restarts: 0}}
	if got := s.Status(); !slices.Equal(got, want) {
		t.Fatalf("Status = %+v, want %+v", got, want)
	}
	pid := s.pids()[0]
	if err := s.Stop(t.Context()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	assertGone(t, pid)
	if got := s.Status(); got[0].Up {
		t.Fatalf("Status after Stop = %+v, want Up=false", got)
	}
}

func TestStartFailsWhenNeverHealthy(t *testing.T) {
	e, versions := helperEngine(t, "cliproxyapi", "never-healthy")
	s := New(testLogger(t), versions, Options{HealthTimeout: 500 * time.Millisecond, HealthInterval: 20 * time.Millisecond})
	stopAtCleanup(t, s)

	pid, errc := startAsync(t.Context(), t, s, e)
	err := <-errc
	if !errors.Is(err, ErrUnhealthy) {
		t.Fatalf("Start = %v, want ErrUnhealthy", err)
	}
	if !strings.Contains(err.Error(), "cliproxyapi") {
		t.Fatalf("error %q does not name the engine", err)
	}
	assertGone(t, pid)
	if got := s.Status(); len(got) != 0 {
		t.Fatalf("Status after failed Start = %+v, want empty", got)
	}
}

func TestStartRefusesMissingOrMismatched(t *testing.T) {
	healthy, versions := helperEngine(t, "bifrost", "healthy")
	missing := healthy
	missing.Bin = "/nonexistent"
	tests := []struct {
		name     string
		engines  []Engine
		versions map[string]EngineVersion
		wantErr  error
	}{
		{name: "missing binary", engines: []Engine{missing}, versions: versions, wantErr: ErrEngineMissing},
		{
			name:     "wrong sha",
			engines:  []Engine{healthy},
			versions: map[string]EngineVersion{"bifrost": {Version: "v1", SHA256: "00"}},
			wantErr:  ErrVersionMismatch,
		},
		{name: "no versions entry", engines: []Engine{healthy}, versions: map[string]EngineVersion{}, wantErr: ErrVersionMismatch},
		{
			// The first engine is valid; nothing may start because the second one fails verification.
			name:     "second engine missing",
			engines:  []Engine{healthy, {Name: "cliproxyapi", Bin: "/nonexistent"}},
			versions: maps.Clone(versions),
			wantErr:  ErrVersionMismatch,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := New(testLogger(t), tt.versions, Options{})
			stopAtCleanup(t, s)
			if err := s.Start(t.Context(), tt.engines...); !errors.Is(err, tt.wantErr) {
				t.Fatalf("Start = %v, want %v", err, tt.wantErr)
			}
			if pids := s.pids(); len(pids) != 0 {
				t.Fatalf("processes started: %v", pids)
			}
			if _, err := os.Stat(filepath.Join(helperDir(healthy), "stops")); err == nil {
				t.Fatal("a helper process was started and stopped")
			}
		})
	}
}

func TestStartCanceledKillsChildren(t *testing.T) {
	e, versions := helperEngine(t, "bifrost", "never-healthy")
	s := New(testLogger(t), versions, Options{HealthInterval: 20 * time.Millisecond})
	stopAtCleanup(t, s)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	pid, errc := startAsync(ctx, t, s, e)
	time.Sleep(200 * time.Millisecond)
	cancel()
	select {
	case err := <-errc:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Start = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return after cancel")
	}
	assertGone(t, pid)
}

func TestRestartsCrashedEngine(t *testing.T) {
	e, versions := helperEngine(t, "cliproxyapi", "crash-after")
	s := New(testLogger(t), versions, Options{
		HealthTimeout:  10 * time.Second,
		HealthInterval: 20 * time.Millisecond,
		BackoffMin:     50 * time.Millisecond,
	})
	stopAtCleanup(t, s)

	if err := s.Start(t.Context(), e); err != nil {
		t.Fatalf("Start: %v", err)
	}
	first := s.pids()[0]
	deadline := time.Now().Add(2 * time.Second)
	for {
		st := s.Status()[0]
		if st.Restarts == 1 && st.Up {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("engine not restarted in time: %+v", st)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if pid := s.pids()[0]; pid == first {
		t.Fatalf("pid unchanged after restart: %d", pid)
	}
	assertGone(t, first)
}

func TestStopReverseOrder(t *testing.T) {
	a, va := helperEngine(t, "a", "healthy")
	b, vb := helperEngine(t, "b", "healthy")
	versions := maps.Clone(va)
	maps.Copy(versions, vb)
	s := New(testLogger(t), versions, Options{HealthInterval: 20 * time.Millisecond})
	stopAtCleanup(t, s)

	if err := s.Start(t.Context(), a, b); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := s.Status(); len(got) != 2 || got[0].Name != "a" || got[1].Name != "b" {
		t.Fatalf("Status = %+v, want a then b", got)
	}
	if err := s.Stop(t.Context()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	ta, tb := stopTime(t, a), stopTime(t, b)
	if tb >= ta {
		t.Fatalf("b stopped at %d, a at %d: want b before a", tb, ta)
	}
}

// stopTime reads the time a helper engine recorded on SIGTERM.
func stopTime(t *testing.T, e Engine) int64 {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(helperDir(e), "stops"))
	if err != nil {
		t.Fatalf("read stops: %v", err)
	}
	name, ts, ok := strings.Cut(strings.TrimSpace(string(b)), " ")
	if !ok || name != e.Name {
		t.Fatalf("stops = %q", b)
	}
	n, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		t.Fatalf("stops = %q: %v", b, err)
	}
	return n
}

func TestStopKillsAfterTimeout(t *testing.T) {
	e, versions := helperEngine(t, "bifrost", "ignore-term")
	s := New(testLogger(t), versions, Options{HealthInterval: 20 * time.Millisecond, StopTimeout: 300 * time.Millisecond})
	stopAtCleanup(t, s)

	if err := s.Start(t.Context(), e); err != nil {
		t.Fatalf("Start: %v", err)
	}
	pid := s.pids()[0]
	begin := time.Now()
	if err := s.Stop(t.Context()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if d := time.Since(begin); d > 2*time.Second {
		t.Fatalf("Stop took %v", d)
	}
	stopTime(t, e) // SIGTERM was delivered first
	assertGone(t, pid)
}

func TestNextBackoff(t *testing.T) {
	tests := []struct {
		attempt int
		want    time.Duration
	}{
		{0, time.Second}, {1, 2 * time.Second}, {2, 4 * time.Second}, {3, 8 * time.Second},
		{4, 16 * time.Second}, {5, 30 * time.Second}, {6, 30 * time.Second}, {100, 30 * time.Second},
		{-1, time.Second},
	}
	for _, tt := range tests {
		if got := nextBackoff(tt.attempt, time.Second, 30*time.Second); got != tt.want {
			t.Errorf("nextBackoff(%d) = %v, want %v", tt.attempt, got, tt.want)
		}
	}
}
