//go:build webdist

package web_test

import (
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/47vigen/remora/internal/web"
)

// TestEmbeddedDist checks the real Vite build embedded in the binary. Run `bun run build` in web/ first.
func TestEmbeddedDist(t *testing.T) {
	h := web.Handler()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), "GET", "/", nil))
	if rec.Code != 200 {
		t.Fatalf("GET / status = %d, want 200; body %q", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `<div id="root">`) {
		t.Fatalf("GET / body has no root element: %q", body)
	}

	asset := regexp.MustCompile(`/assets/[^"]+\.js`).FindString(body)
	if asset == "" {
		t.Fatalf("GET / body references no script asset: %q", body)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), "GET", asset, nil))
	if rec.Code != 200 {
		t.Fatalf("GET %s status = %d, want 200", asset, rec.Code)
	}
	if got, want := rec.Header().Get("Cache-Control"), "public, max-age=31536000, immutable"; got != want {
		t.Errorf("GET %s Cache-Control = %q, want %q", asset, got, want)
	}
	if got, want := rec.Header().Get("Content-Type"), "text/javascript; charset=utf-8"; got != want {
		t.Errorf("GET %s Content-Type = %q, want %q", asset, got, want)
	}
}
