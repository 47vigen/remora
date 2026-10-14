package main

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthcheck(t *testing.T) {
	status := func(code int) string {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) }))
		t.Cleanup(srv.Close)
		return srv.URL + "/healthz"
	}
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := "http://" + ln.Addr().String() + "/healthz"
	_ = ln.Close()

	tests := []struct {
		name     string
		url      string
		wantCode int
		wantOut  string
	}{
		{"ok", status(http.StatusOK), 0, "200\n"},
		{"not found", status(http.StatusNotFound), 1, "404\n"},
		{"degraded", status(http.StatusServiceUnavailable), 1, "503\n"},
		{"closed port", closed, 1, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			if code := healthcheck(context.Background(), tt.url, &out); code != tt.wantCode {
				t.Errorf("code = %d, want %d", code, tt.wantCode)
			}
			if tt.wantOut != "" && out.String() != tt.wantOut {
				t.Errorf("stdout = %q, want %q", out.String(), tt.wantOut)
			}
		})
	}
}

func TestDefaultHealthURL(t *testing.T) {
	tests := []struct{ listen, want string }{
		{"", "http://127.0.0.1:8080/healthz"},
		{":9000", "http://127.0.0.1:9000/healthz"},
		{"0.0.0.0:9001", "http://127.0.0.1:9001/healthz"},
		{"[::]:9002", "http://127.0.0.1:9002/healthz"},
		{"127.0.0.2:9003", "http://127.0.0.2:9003/healthz"},
		{"[::1]:9004", "http://[::1]:9004/healthz"},
	}
	for _, tt := range tests {
		if got := defaultHealthURL(tt.listen); got != tt.want {
			t.Errorf("defaultHealthURL(%q) = %q, want %q", tt.listen, got, tt.want)
		}
	}
}
