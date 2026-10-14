package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"time"

	"github.com/47vigen/remora/internal/api"
	"github.com/47vigen/remora/internal/auth"
	"github.com/47vigen/remora/internal/config"
	"github.com/47vigen/remora/internal/proxy"
	"github.com/47vigen/remora/internal/reconcile"
	"github.com/47vigen/remora/internal/store"
	"github.com/47vigen/remora/internal/supervise"
)

const (
	readHeaderTimeout = 10 * time.Second
	shutdownTimeout   = 15 * time.Second
	engineStopTimeout = 45 * time.Second // covers both engines' 15s SIGTERM grace
)

// runServe implements `remora serve`.
func runServe(ctx context.Context, getenv func(string) string, stderr io.Writer) int {
	cfg, err := config.Load(getenv)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	logger := slog.New(slog.NewJSONHandler(stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))
	if err := serve(ctx, cfg, logger); err != nil {
		logger.Error("serve failed", "err", err)
		return 1
	}
	return 0
}

// serve prepares the data directory, the store and the engine credentials, starts the engines and then serves the
// public listener until ctx ends. It shuts down the listener, then the engines, then the store.
func serve(ctx context.Context, cfg config.Config, logger *slog.Logger) (err error) {
	if err := prepareDataDir(cfg.DataDir); err != nil {
		return fmt.Errorf("prepare data dir: %w", err)
	}

	st, err := store.Open(ctx, cfg.DBPath())
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer func() {
		if cerr := st.Close(); cerr != nil {
			err = errors.Join(err, fmt.Errorf("close store: %w", cerr))
		}
	}()

	pw, err := auth.EnsureAdmin(ctx, st, cfg.AdminPassword)
	if err != nil {
		return fmt.Errorf("ensure admin: %w", err)
	}
	if pw != "" {
		logger.Log(ctx, passwordLogLevel(ctx, logger), "admin password generated; it is shown only this once", "password", pw)
	}

	// Secrets at rest: load (or create) the master key before anything else needs the sealer.
	if err := setSealer(st, cfg); err != nil {
		return fmt.Errorf("master key: %w", err)
	}

	creds, err := supervise.EnsureCredentials(ctx, st)
	if err != nil {
		return fmt.Errorf("ensure engine credentials: %w", err)
	}
	cliproxy, err := supervise.CLIProxyAPIEngine(cfg, creds)
	if err != nil {
		return fmt.Errorf("render engine config: %w", err)
	}
	bifrost, err := supervise.BifrostEngine(cfg, creds)
	if err != nil {
		return fmt.Errorf("render engine config: %w", err)
	}

	versions, err := supervise.LoadVersions(filepath.Join(cfg.EngineDir, "versions.json"))
	if err != nil {
		return fmt.Errorf("load engine versions: %w", err)
	}

	sup := supervise.New(logger, versions, supervise.Options{})
	defer func() {
		// ctx is already done here; the engines get their own deadline.
		sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), engineStopTimeout)
		defer cancel()
		if serr := sup.Stop(sctx); serr != nil {
			err = errors.Join(err, fmt.Errorf("stop engines: %w", serr))
		}
	}()
	// The OAuth engine starts first because the front door calls it; it stops last.
	if err := sup.Start(ctx, cliproxy, bifrost); err != nil {
		return fmt.Errorf("start engines: %w", err)
	}

	// Reconcile the front door now that it is healthy, before the listener opens, so /v1 never meets an engine that
	// lags the store (a fresh or wiped engine data directory). A failure is logged, not fatal: the timer retries,
	// and /v1 stays closed (503) until a pass succeeds.
	// Then reconcile on a timer; the goroutine ends before the engines stop and the store closes.
	rec := &reconcile.Reconciler{Store: st, Engine: frontDoor(cfg, creds), Log: logger}
	if err := rec.Sync(ctx); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		logger.Error("initial reconcile failed; retrying on the timer", "err", err)
	}
	rctx, rcancel := context.WithCancel(ctx)
	reconciled := make(chan struct{})
	go func() {
		defer close(reconciled)
		rec.Run(rctx)
	}()
	defer func() {
		rcancel()
		<-reconciled
	}()

	upstream, err := url.Parse(frontDoorURL(cfg))
	if err != nil {
		return fmt.Errorf("front door url: %w", err)
	}
	mux := http.NewServeMux()
	api.RegisterSystem(mux, sup)
	// /v1 answers 503 until a Sync has completed, so a failed first pass never serves a stale front door; the
	// management API and health stay reachable meanwhile.
	px := proxy.New(upstream, st, logger)
	px.Ready = rec.Synced
	mux.Handle("/v1/", px)
	srv := newServer(ctx, cfg.Listen, mux)
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", cfg.Listen)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.Listen, err)
	}
	logger.Info("remora listening", "addr", ln.Addr().String())

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

	select {
	case err := <-serveErr:
		return fmt.Errorf("serve http: %w", err)
	case <-ctx.Done():
	}
	logger.Info("shutting down")
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(sctx); err != nil {
		return fmt.Errorf("shut down http: %w", err)
	}
	return nil
}

// passwordLogLevel is the level of the one-time admin password line: warn, raised to error when the configured
// level would hide a warning, so the only copy of the password is never filtered out.
func passwordLogLevel(ctx context.Context, logger *slog.Logger) slog.Level {
	if logger.Enabled(ctx, slog.LevelWarn) {
		return slog.LevelWarn
	}
	return slog.LevelError
}

// newServer is the public listener's server. It has no ReadTimeout or WriteTimeout: /v1 streams answers for as
// long as the upstream takes, and uploads may be large. ReadHeaderTimeout still bounds a slow client's headers.
func newServer(ctx context.Context, addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: readHeaderTimeout,
		BaseContext:       func(net.Listener) context.Context { return context.WithoutCancel(ctx) },
	}
}
