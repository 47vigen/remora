package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRemoteIP(t *testing.T) {
	for _, c := range []struct{ peer, realIP, want string }{
		{"203.0.113.7:4000", "", "203.0.113.7"},
		{"203.0.113.7:4000", "198.51.100.1", "203.0.113.7"}, // a remote client cannot claim another address
		{"127.0.0.1:4000", "198.51.100.1", "198.51.100.1"},  // the image's nginx reports the client
		{"[::1]:4000", "2001:db8::1", "2001:db8::1"},
		{"127.0.0.1:4000", "", "127.0.0.1"},
		{"127.0.0.1:4000", "not an ip", "127.0.0.1"},
	} {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/auth/login", nil)
		r.RemoteAddr = c.peer
		if c.realIP != "" {
			r.Header.Set("X-Real-IP", c.realIP)
		}
		if got := remoteIP(r); got != c.want {
			t.Errorf("peer %s X-Real-IP %q = %q, want %q", c.peer, c.realIP, got, c.want)
		}
	}
}

func TestRemoteIPProxyChain(t *testing.T) {
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/auth/login", nil)
	r.RemoteAddr = "127.0.0.1:4000"
	r.Header.Set("X-Forwarded-For", "192.0.2.99, 198.51.100.1, 10.0.0.2")
	r.Header.Set("X-Real-IP", "198.51.100.1") // Resolved by nginx realip, not by Go.
	if got := remoteIP(r); got != "198.51.100.1" {
		t.Errorf("remoteIP = %q, want 198.51.100.1", got)
	}
	r.Header.Del("X-Real-IP")
	if got := remoteIP(r); got != "127.0.0.1" {
		t.Errorf("remoteIP with only X-Forwarded-For = %q, want 127.0.0.1", got)
	}
}
