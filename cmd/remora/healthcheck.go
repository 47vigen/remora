package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/47vigen/remora/internal/config"
)

const healthcheckTimeout = 3 * time.Second

// runHealthcheck implements `remora healthcheck [url]`.
func runHealthcheck(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	switch len(args) {
	case 0:
		return healthcheck(ctx, defaultHealthURL(getenv(config.EnvListen)), stdout)
	case 1:
		return healthcheck(ctx, args[0], stdout)
	default:
		_, _ = fmt.Fprintln(stderr, "usage: remora healthcheck [url]")
		return 2
	}
}

// defaultHealthURL is /healthz on the port of REMORA_LISTEN. A wildcard or empty host becomes 127.0.0.1, so the
// probe works from inside the container.
func defaultHealthURL(listen string) string {
	if listen == "" {
		listen = config.DefaultListen
	}
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		host, port = "", "8080"
	}
	switch host {
	case "", "0.0.0.0", "::":
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port) + "/healthz"
}

// healthcheck GETs url and prints the status code. It returns 0 for a 2xx answer and 1 otherwise.
func healthcheck(ctx context.Context, url string, stdout io.Writer) int {
	ctx, cancel := context.WithTimeout(ctx, healthcheckTimeout)
	defer cancel()
	// The operator chooses the URL; probing it is the point of the command.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody) //nolint:gosec // G704: see above.
	if err != nil {
		_, _ = fmt.Fprintln(stdout, err)
		return 1
	}
	resp, err := http.DefaultClient.Do(req) //nolint:gosec // G704: see above.
	if err != nil {
		_, _ = fmt.Fprintln(stdout, err)
		return 1
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	_, _ = fmt.Fprintln(stdout, resp.StatusCode)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return 1
	}
	return 0
}
