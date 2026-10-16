package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
)

// startPprof serves net/http/pprof on addr, a loopback address config.Load has checked, on its own server so the
// profiles never reach the public listener. An empty addr starts nothing. stop shuts it down.
func startPprof(ctx context.Context, addr string, logger *slog.Logger) (stop func(), bound string, err error) {
	if addr == "" {
		return func() {}, "", nil
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", addr)
	if err != nil {
		return nil, "", fmt.Errorf("pprof: listen on %s: %w", addr, err)
	}
	srv := newServer(ctx, addr, mux)
	go func() { _ = srv.Serve(ln) }()
	logger.Info("pprof listening", "addr", ln.Addr().String())
	return func() { _ = srv.Close() }, ln.Addr().String(), nil
}
