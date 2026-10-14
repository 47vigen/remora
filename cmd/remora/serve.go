package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"time"

	"github.com/47vigen/remora/internal/api"
	"github.com/47vigen/remora/internal/auth"
	"github.com/47vigen/remora/internal/config"
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
		logger.Warn("admin password generated; it is shown only this once", "password", pw)
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

	mux := http.NewServeMux()
	api.RegisterSystem(mux, sup)
	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           mux,
		ReadHeaderTimeout: readHeaderTimeout,
		BaseContext:       func(net.Listener) context.Context { return context.WithoutCancel(ctx) },
	}
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
