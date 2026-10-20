package supervise

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os/exec"
	"slices"
	"sync"
	"syscall"
	"time"
)

// Default Options values.
const (
	defaultHealthTimeout  = 120 * time.Second // Bifrost's first boot runs its migrations
	defaultHealthInterval = 250 * time.Millisecond
	defaultStopTimeout    = 15 * time.Second
	defaultMinBackoff     = time.Second
	defaultMaxBackoff     = 30 * time.Second
	defaultStableAfter    = 60 * time.Second

	probeTimeout  = 2 * time.Second // upper bound for one health request
	waitDelay     = 2 * time.Second // how long Wait waits for output pipes after the child exits
	maxLineLength = 64 * 1024       // longer output lines are logged in pieces
)

var errStopped = errors.New("supervise: supervisor is stopped")

// Engine is one child process to supervise.
type Engine struct {
	Name      string   // "bifrost" | "cliproxyapi"
	Bin       string   // path to the binary, checked against versions.json
	Args, Env []string // Env is the child's complete environment (exec.Cmd.Env semantics)
	HealthURL string   // answers 2xx once the engine is ready
}

// Options tunes the supervisor. Zero fields take the defaults noted on each field.
type Options struct {
	HealthTimeout  time.Duration // 120s: Bifrost's first boot runs its migrations
	HealthInterval time.Duration // 250ms
	StopTimeout    time.Duration // 15s, then SIGKILL
	BackoffMin     time.Duration // 1s
	BackoffMax     time.Duration // 30s
	StableAfter    time.Duration // 60s healthy resets the backoff
}

func (o Options) withDefaults() Options {
	def := func(d *time.Duration, v time.Duration) {
		if *d <= 0 {
			*d = v
		}
	}
	def(&o.HealthTimeout, defaultHealthTimeout)
	def(&o.HealthInterval, defaultHealthInterval)
	def(&o.StopTimeout, defaultStopTimeout)
	def(&o.BackoffMin, defaultMinBackoff)
	def(&o.BackoffMax, defaultMaxBackoff)
	def(&o.StableAfter, defaultStableAfter)
	return o
}

// EngineStatus is a snapshot of one supervised engine.
type EngineStatus struct {
	Name     string `json:"name"`
	Version  string `json:"version"`
	Up       bool   `json:"up"`
	Restarts int    `json:"restarts"`
}

// Supervisor runs engines as child processes. Create it with New.
type Supervisor struct {
	logger   *slog.Logger
	versions map[string]EngineVersion
	opts     Options
	client   *http.Client

	quit chan struct{}  // closed by Stop
	kill chan struct{}  // closed when the ctx of the first Stop ends
	wg   sync.WaitGroup // in-flight Start calls and restart monitors

	mu      sync.Mutex
	procs   []*proc // in start order
	stopped bool
}

// proc is one engine and its current child process. All mutable fields are guarded by Supervisor.mu.
type proc struct {
	eng     Engine
	version string
	log     *slog.Logger

	cmd      *exec.Cmd
	done     chan struct{} // closed once cmd has exited and its output is flushed
	up       bool
	restarts int
}

// New returns a Supervisor that checks engines against versions. Zero fields of opts take their defaults.
func New(logger *slog.Logger, versions map[string]EngineVersion, opts Options) *Supervisor {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Supervisor{
		logger:   logger,
		versions: versions,
		opts:     opts.withDefaults(),
		client:   &http.Client{},
		quit:     make(chan struct{}),
		kill:     make(chan struct{}),
	}
}

// Start verifies every engine against versions.json, then launches them in order, each after the previous one is
// healthy. It returns once all are healthy. On failure, or when ctx is canceled, it stops every child it started.
// After Start succeeds, an engine that exits is restarted with exponential backoff until Stop.
func (s *Supervisor) Start(ctx context.Context, engines ...Engine) error {
	for _, e := range engines {
		if err := s.verify(e); err != nil {
			return err
		}
	}

	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return errStopped
	}
	s.wg.Add(1)
	s.mu.Unlock()
	defer s.wg.Done()

	ctx, cancel := s.withClose(ctx, s.quit)
	defer cancel()

	started := make([]*proc, 0, len(engines))
	for _, e := range engines {
		p := &proc{eng: e, version: s.versions[e.Name].Version, log: s.logger.With("engine", e.Name)}
		s.mu.Lock()
		s.procs = append(s.procs, p)
		s.mu.Unlock()
		started = append(started, p)

		err := s.launch(ctx, p)
		if err == nil {
			err = s.waitHealthy(ctx, p)
		}
		if err != nil {
			// A concurrent Stop waits for this cleanup, so its deadline bounds it too.
			actx, acancel := s.withClose(context.WithoutCancel(ctx), s.kill)
			s.abort(actx, started)
			acancel()
			return err
		}
		s.mu.Lock()
		p.up = true
		s.mu.Unlock()
		p.log.Info("engine healthy", "version", p.version)
	}

	for _, p := range started {
		mctx, mcancel := s.withClose(context.WithoutCancel(ctx), s.quit)
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer mcancel()
			s.monitor(mctx, p)
		}()
	}
	return nil
}

// Stop stops the engines in reverse start order: SIGTERM, then SIGKILL after StopTimeout or when ctx ends. It
// waits for an in-flight Start to return first. Stop is idempotent; the Supervisor cannot be started again.
func (s *Supervisor) Stop(ctx context.Context) error {
	s.mu.Lock()
	if !s.stopped {
		s.stopped = true
		close(s.quit)
		context.AfterFunc(ctx, func() { close(s.kill) })
	}
	s.mu.Unlock()
	s.wg.Wait()

	s.mu.Lock()
	procs := slices.Clone(s.procs)
	s.mu.Unlock()
	var errs []error
	for _, p := range slices.Backward(procs) {
		if err := s.terminate(ctx, p); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Status returns a snapshot of every engine, in start order.
func (s *Supervisor) Status() []EngineStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]EngineStatus, 0, len(s.procs))
	for _, p := range s.procs {
		out = append(out, EngineStatus{Name: p.eng.Name, Version: p.version, Up: p.up, Restarts: p.restarts})
	}
	return out
}

// pids returns the pid of each engine's current child, in start order. Tests use it.
func (s *Supervisor) pids() []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []int
	for _, p := range s.procs {
		if p.cmd != nil && p.cmd.Process != nil {
			out = append(out, p.cmd.Process.Pid)
		}
	}
	return out
}

// nextBackoff returns lo * 2^attempt, capped at hi.
func nextBackoff(attempt int, lo, hi time.Duration) time.Duration {
	d := lo
	for i := 0; i < attempt && d < hi; i++ {
		d *= 2
	}
	return min(d, hi)
}

func (s *Supervisor) verify(e Engine) error {
	want, ok := s.versions[e.Name]
	if !ok {
		return fmt.Errorf("%w: no entry for %s", ErrVersionMismatch, e.Name)
	}
	if err := VerifyBinary(e.Bin, want); err != nil {
		return fmt.Errorf("supervise: verify %s: %w", e.Name, err)
	}
	return nil
}

// withClose returns a context that is also canceled when ch is closed.
func (s *Supervisor) withClose(parent context.Context, ch <-chan struct{}) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	go func() {
		select {
		case <-ch:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}

// launch starts a new child for p and a goroutine that reaps it.
func (s *Supervisor) launch(ctx context.Context, p *proc) error {
	// The child's lifetime is managed by terminate, never by context cancellation.
	// #nosec G204 -- the binary was verified against versions.json and the arguments are built by remora
	cmd := exec.CommandContext(context.WithoutCancel(ctx), p.eng.Bin, p.eng.Args...)
	cmd.Env = p.eng.Env
	cmd.SysProcAttr = sysProcAttr()
	stdout := &lineLogger{log: p.log, stream: "stdout"}
	stderr := &lineLogger{log: p.log, stream: "stderr"}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	cmd.WaitDelay = waitDelay
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("supervise: start %s: %w", p.eng.Name, err)
	}
	done := make(chan struct{})
	s.mu.Lock()
	p.cmd, p.done, p.up = cmd, done, false
	s.mu.Unlock()
	p.log.Info("engine started", "pid", cmd.Process.Pid)

	go func() {
		err := cmd.Wait()
		stdout.flush()
		stderr.flush()
		p.log.Info("engine exited", "pid", cmd.Process.Pid, "err", err)
		close(done)
	}()
	return nil
}

// waitHealthy polls p's health URL until it answers 2xx, the child exits, HealthTimeout passes or ctx ends.
func (s *Supervisor) waitHealthy(ctx context.Context, p *proc) error {
	s.mu.Lock()
	done := p.done
	s.mu.Unlock()

	hctx, cancel := context.WithTimeout(ctx, s.opts.HealthTimeout)
	defer cancel()
	ticker := time.NewTicker(s.opts.HealthInterval)
	defer ticker.Stop()
	for {
		if s.probe(hctx, p.eng.HealthURL) {
			return nil
		}
		select {
		case <-done:
			return fmt.Errorf("%w: %s exited before it became healthy", ErrUnhealthy, p.eng.Name)
		case <-hctx.Done():
			if err := ctx.Err(); err != nil {
				return fmt.Errorf("supervise: start %s: %w", p.eng.Name, err)
			}
			return fmt.Errorf("%w: %s not healthy at %s after %s", ErrUnhealthy, p.eng.Name, p.eng.HealthURL,
				s.opts.HealthTimeout)
		case <-ticker.C:
		}
	}
}

// probe reports whether one GET of url answers 2xx.
func (s *Supervisor) probe(ctx context.Context, url string) bool {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return false
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64*1024))
	_ = resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

// abort stops the children of a failed Start in reverse order and forgets them.
func (s *Supervisor) abort(ctx context.Context, started []*proc) {
	for _, p := range slices.Backward(started) {
		if err := s.terminate(ctx, p); err != nil {
			p.log.Error("stop after failed start", "err", err)
		}
	}
	s.mu.Lock()
	s.procs = slices.DeleteFunc(s.procs, func(p *proc) bool { return slices.Contains(started, p) })
	s.mu.Unlock()
}

// terminate sends SIGTERM to p's child and waits for it to exit. After StopTimeout, or when ctx ends, it sends
// SIGKILL. It returns once the child has been reaped.
func (s *Supervisor) terminate(ctx context.Context, p *proc) error {
	s.mu.Lock()
	cmd, done := p.cmd, p.done
	p.up = false
	s.mu.Unlock()
	if cmd == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	default:
	}

	p.log.Info("stopping engine", "pid", cmd.Process.Pid)
	if err := signalProcess(cmd.Process, syscall.SIGTERM); err != nil {
		p.log.Warn("sigterm failed", "err", err)
	}
	timer := time.NewTimer(s.opts.StopTimeout)
	defer timer.Stop()
	var result error
	select {
	case <-done:
		return nil
	case <-timer.C:
		p.log.Warn("engine ignored SIGTERM, killing it", "timeout", s.opts.StopTimeout)
	case <-ctx.Done():
		result = fmt.Errorf("supervise: stop %s: %w", p.eng.Name, ctx.Err())
	}
	if err := signalProcess(cmd.Process, syscall.SIGKILL); err != nil {
		return fmt.Errorf("supervise: kill %s: %w", p.eng.Name, err)
	}
	<-done
	return result
}

// monitor restarts p's child each time it exits, until ctx ends. The backoff grows with each restart and resets
// once the engine has stayed up for StableAfter.
func (s *Supervisor) monitor(ctx context.Context, p *proc) {
	attempt := 0
	healthySince := time.Now()
	for {
		s.mu.Lock()
		done := p.done
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-done:
		}

		s.mu.Lock()
		p.up = false
		s.mu.Unlock()
		if !healthySince.IsZero() && time.Since(healthySince) >= s.opts.StableAfter {
			attempt = 0
		}
		healthySince = time.Time{}
		delay := nextBackoff(attempt, s.opts.BackoffMin, s.opts.BackoffMax)
		attempt++
		p.log.Warn("engine down, restarting", "in", delay, "attempt", attempt)
		if !sleep(ctx, delay) {
			return
		}

		s.mu.Lock()
		p.restarts++
		s.mu.Unlock()
		if err := s.launch(ctx, p); err != nil {
			p.log.Error("restart failed", "err", err)
			// p.done still points at the exited child, so the loop backs off and tries again.
			continue
		}
		if err := s.waitHealthy(ctx, p); err != nil {
			if ctx.Err() != nil {
				return // Stop terminates the child.
			}
			p.log.Error("restarted engine not healthy", "err", err)
			if err := s.terminate(ctx, p); err != nil {
				p.log.Error("stop unhealthy engine", "err", err)
			}
			continue
		}
		s.mu.Lock()
		p.up = true
		s.mu.Unlock()
		healthySince = time.Now()
		p.log.Info("engine healthy again", "restarts", attempt)
	}
}

// sleep waits for d and reports false when ctx ends first.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// lineLogger logs a child's output line by line. exec.Cmd writes each stream from one goroutine.
type lineLogger struct {
	log    *slog.Logger
	stream string
	buf    []byte
	carry  *slog.Level // the level of a record split at maxLineLength, for its continuation
}

func (l *lineLogger) Write(b []byte) (int, error) {
	l.buf = append(l.buf, b...)
	for {
		i := bytes.IndexByte(l.buf, '\n')
		if i < 0 {
			break
		}
		l.emit(l.buf[:i], false)
		l.buf = l.buf[i+1:]
	}
	if len(l.buf) >= maxLineLength {
		l.emit(l.buf, true)
		l.buf = nil
	}
	return len(b), nil
}

// flush logs a final line that had no newline.
func (l *lineLogger) flush() {
	if len(l.buf) > 0 {
		l.emit(l.buf, false)
		l.buf = nil
	}
}

func (l *lineLogger) emit(line []byte, split bool) {
	line = bytes.TrimRight(line, "\r")
	level := lineLevel(line)
	if l.carry != nil {
		level = *l.carry
	}
	l.carry = nil
	if split {
		l.carry = &level
	}
	if len(line) == 0 {
		return
	}
	log := l.log.Info
	switch level {
	case slog.LevelDebug:
		log = l.log.Debug
	case slog.LevelWarn:
		log = l.log.Warn
	case slog.LevelError:
		log = l.log.Error
	}
	log("engine output", "stream", l.stream, "line", string(line))
}

// lineLevel reads the level an engine wrote into its own line. Its info lines, an access log line per call, drop to
// debug; a line without a level stays info.
func lineLevel(line []byte) slog.Level {
	for _, m := range []struct {
		marks []string
		level slog.Level
	}{
		{[]string{`[error]`, `[fatal]`, `[panic]`, `"level":"error"`, `"level":"fatal"`, `"level":"panic"`}, slog.LevelError},
		{[]string{`[warn ]`, `"level":"warn"`}, slog.LevelWarn},
		{[]string{`[info ]`, `[debug]`, `"level":"info"`, `"level":"debug"`}, slog.LevelDebug},
	} {
		for _, mark := range m.marks {
			if bytes.Contains(line, []byte(mark)) {
				return m.level
			}
		}
	}
	return slog.LevelInfo
}
