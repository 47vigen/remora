// Command fakes serves scripted fake upstreams for the e2e tests.
//
//	fakes             serve on FAKES_LISTEN (default :9090)
//	fakes get <url>   print the status code; exit 0 on 2xx, else 1
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/47vigen/remora/test/fakes/control"
)

const defaultListen = ":9090"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch {
	case len(args) == 0:
		logger := slog.New(slog.NewJSONHandler(stderr, nil))
		if err := serve(ctx, logger, listenAddr()); err != nil {
			logger.Error("fakes stopped", "err", err)
			return 1
		}
		return 0
	case args[0] == "get" && len(args) == 2:
		return get(ctx, args[1], stdout, stderr)
	default:
		_, _ = fmt.Fprintln(stderr, "usage: fakes | fakes get <url>")
		return 2
	}
}

func listenAddr() string {
	if v := os.Getenv("FAKES_LISTEN"); v != "" {
		return v
	}
	return defaultListen
}

func serve(ctx context.Context, logger *slog.Logger, addr string) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           control.New(control.Defaults()).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	errc := make(chan error, 1)
	go func() {
		logger.Info("fakes listening", "addr", addr)
		errc <- srv.ListenAndServe()
	}()
	select {
	case err := <-errc:
		return fmt.Errorf("listen %s: %w", addr, err)
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	if err := <-errc; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	return nil
}

func get(ctx context.Context, url string, stdout, stderr io.Writer) int {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	//nolint:gosec // G704: url is the operator's own command-line argument; fetching it is the point of `fakes get`.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "fakes get:", err)
		return 1
	}
	//nolint:gosec // G704: requests the operator-supplied URL from the `fakes get` command line.
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "fakes get:", err)
		return 1
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	_, _ = fmt.Fprintln(stdout, resp.StatusCode)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return 1
	}
	return 0
}
