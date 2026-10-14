package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMiddleware(t *testing.T) {
	s, _, _ := newService(t)
	tok := login(t, s, testPassword)
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := s.Middleware(func(r *http.Request) bool { return r.URL.Path == "/api/health" })(ok)

	do := func(method, path string, setup func(*http.Request)) *httptest.ResponseRecorder {
		r := httptest.NewRequestWithContext(context.Background(), method, "http://remora.test"+path, http.NoBody)
		if setup != nil {
			setup(r)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec
	}
	withCookie := func(r *http.Request) { addSession(r, tok) }

	rec := do(http.MethodPost, "/api/providers", func(r *http.Request) {
		withCookie(r)
		r.Header.Set("Sec-Fetch-Site", "cross-site")
	})
	if rec.Code != http.StatusForbidden {
		t.Errorf("cross-site POST = %d, want 403", rec.Code)
	}
	if code := errorCode(t, rec); code != "forbidden" {
		t.Errorf("cross-site POST code = %q, want forbidden", code)
	}

	rec = do(http.MethodPost, "/api/providers", func(r *http.Request) {
		withCookie(r)
		r.Header.Set("Sec-Fetch-Site", "same-origin")
	})
	if rec.Code != http.StatusOK {
		t.Errorf("same-origin POST = %d, want 200", rec.Code)
	}

	if rec := do(http.MethodGet, "/api/health", nil); rec.Code != http.StatusOK {
		t.Errorf("public path = %d, want 200", rec.Code)
	}

	rec = do(http.MethodGet, "/api/providers", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("no auth = %d, want 401", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	if code := errorCode(t, rec); code != "unauthorized" {
		t.Errorf("no auth code = %q, want unauthorized", code)
	}
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return body.Error.Code
}
