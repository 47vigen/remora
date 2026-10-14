package web_test

import (
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/47vigen/remora/internal/web"
)

func testFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html":           {Data: []byte(`<!doctype html><div id="root"></div><script src="/assets/app-abc123.js"></script>`)},
		"assets/app-abc123.js": {Data: []byte("console.log(1)")},
		"robots.txt":           {Data: []byte("User-agent: *")},
	}
}

func TestHandler(t *testing.T) {
	h := web.New(testFS())
	tests := []struct {
		name, method, path string
		status             int
		cache, ctype, body string // "" = not checked
	}{
		{"root", "GET", "/", 200, "no-cache", "text/html; charset=utf-8", `<div id="root">`},
		{"spa route", "GET", "/logs", 200, "no-cache", "text/html; charset=utf-8", `<div id="root">`},
		{"spa nested", "GET", "/providers/abc", 200, "no-cache", "", `<div id="root">`},
		{"index direct", "GET", "/index.html", 200, "no-cache", "", `<div id="root">`},
		{"asset", "GET", "/assets/app-abc123.js", 200, "public, max-age=31536000, immutable", "text/javascript; charset=utf-8", "console.log"},
		{"other file", "GET", "/robots.txt", 200, "public, max-age=3600", "", "User-agent"},
		{"missing asset", "GET", "/assets/gone.js", 404, "", "", ""},
		{"missing file with ext", "GET", "/favicon.ico", 404, "", "", ""},
		{"api typo", "GET", "/api/nope", 404, "", "", ""},
		{"v1 typo", "GET", "/v1/nope", 404, "", "", ""},
		{"post", "POST", "/", 405, "", "", ""},
		{"head", "HEAD", "/", 200, "no-cache", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), tt.method, tt.path, nil))
			if rec.Code != tt.status {
				t.Fatalf("status = %d, want %d", rec.Code, tt.status)
			}
			if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
				t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
			}
			if tt.cache != "" {
				if got := rec.Header().Get("Cache-Control"); got != tt.cache {
					t.Errorf("Cache-Control = %q, want %q", got, tt.cache)
				}
			}
			if tt.ctype != "" {
				if got := rec.Header().Get("Content-Type"); got != tt.ctype {
					t.Errorf("Content-Type = %q, want %q", got, tt.ctype)
				}
			}
			if tt.body != "" && !strings.Contains(rec.Body.String(), tt.body) {
				t.Errorf("body = %q, want it to contain %q", rec.Body.String(), tt.body)
			}
			if tt.status == 405 {
				if got := rec.Header().Get("Allow"); got != "GET, HEAD" {
					t.Errorf("Allow = %q, want %q", got, "GET, HEAD")
				}
			}
			if tt.method == "HEAD" && rec.Body.Len() != 0 {
				t.Errorf("HEAD body = %q, want empty", rec.Body.String())
			}
		})
	}
}

func TestHandlerNotBuilt(t *testing.T) {
	h := web.New(fstest.MapFS{".gitkeep": {}})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), "GET", "/", nil))
	if rec.Code != 503 {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if want := "web UI is not built: run `bun run build` in web/"; !strings.Contains(rec.Body.String(), want) {
		t.Errorf("body = %q, want it to contain %q", rec.Body.String(), want)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
}

func TestHandlerIndexRevalidates(t *testing.T) {
	h := web.New(testFS())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), "GET", "/", nil))
	etag := rec.Header().Get("ETag")
	if etag == "" {
		t.Fatal("index has no ETag")
	}
	req := httptest.NewRequestWithContext(t.Context(), "GET", "/logs", nil)
	req.Header.Set("If-None-Match", etag)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 304 {
		t.Fatalf("status = %d, want 304", rec.Code)
	}
}
