package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/47vigen/remora/internal/supervise"
	"github.com/47vigen/remora/internal/version"
)

type fakeStatus []supervise.EngineStatus

func (f fakeStatus) Status() []supervise.EngineStatus { return f }

func both(cliproxyUp bool) fakeStatus {
	return fakeStatus{
		{Name: "bifrost", Version: "transports/v2.3.0", Up: true},
		{Name: "cliproxyapi", Version: "v8.0.28", Up: cliproxyUp},
	}
}

func serve(t *testing.T, src StatusSource, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	RegisterSystem(mux, src)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), method, path, http.NoBody))
	return rec
}

func checkHealthz(t *testing.T, src fakeStatus, wantCode int, wantBody string) {
	t.Helper()
	rec := serve(t, src, http.MethodGet, "/healthz")
	if rec.Code != wantCode {
		t.Errorf("code = %d, want %d", rec.Code, wantCode)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != wantBody {
		t.Errorf("body = %s, want %s", got, wantBody)
	}
}

func TestHealthzOK(t *testing.T) {
	checkHealthz(t, both(true), http.StatusOK, `{"status":"ok"}`)
}

func TestHealthzDegraded(t *testing.T) {
	checkHealthz(t, both(false), http.StatusServiceUnavailable, `{"status":"degraded","down":["accounts"]}`)
}

func TestSystemVersion(t *testing.T) {
	rec := serve(t, both(false), http.MethodGet, "/api/system/version")
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	var got VersionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Remora != version.Version {
		t.Errorf("remora = %q, want %q", got.Remora, version.Version)
	}
	want := []EngineVersionInfo{{"bifrost", "transports/v2.3.0"}, {"cliproxyapi", "v8.0.28"}}
	if !slices.Equal(got.Engines, want) {
		t.Errorf("engines = %+v, want %+v", got.Engines, want)
	}

	if rec := serve(t, both(true), http.MethodPost, "/api/system/version"); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST code = %d, want 405", rec.Code)
	}
}
