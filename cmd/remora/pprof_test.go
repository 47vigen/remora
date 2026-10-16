package main

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"testing"
)

func TestStartPprof(t *testing.T) {
	ctx := context.Background()
	stop, addr, err := startPprof(ctx, "", slog.New(slog.DiscardHandler))
	if err != nil || addr != "" {
		t.Fatalf("off: addr %q err %v, want nothing started", addr, err)
	}
	stop()

	stop, addr, err = startPprof(ctx, "127.0.0.1:0", slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("startPprof: %v", err)
	}
	defer stop()
	if host, _, _ := net.SplitHostPort(addr); host != "127.0.0.1" {
		t.Fatalf("addr = %q, want loopback", addr)
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/debug/pprof/cmdline", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}
