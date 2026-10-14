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
	RegisterHealthz(mux, src)
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

func TestSystemAdapter(t *testing.T) {
	sys := NewSystem(both(false))
	remora, engines := sys.Version()
	if remora != version.Version {
		t.Errorf("remora = %q, want %q", remora, version.Version)
	}
	want := []EngineVersion{{"bifrost", "transports/v2.3.0"}, {"cliproxyapi", "v8.0.28"}}
	if !slices.Equal(engines, want) {
		t.Errorf("engines = %+v, want %+v", engines, want)
	}
	ok, health := sys.Health(context.Background())
	if ok || health["router"] != nil || health["accounts"] == nil || len(health) != 2 {
		t.Errorf("health = %v %v, want router ok and accounts down", ok, health)
	}
}

func TestVersionAnswersThroughServer(t *testing.T) {
	srv := httptest.NewServer(New(Deps{System: NewSystem(both(true))}).Handler())
	t.Cleanup(srv.Close)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/api/system/version", http.NoBody)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("code = %d, content type %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	var got struct {
		Remora  string          `json:"remora"`
		Engines []EngineVersion `json:"engines"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Remora != version.Version || len(got.Engines) != 2 || got.Engines[0].Name != "bifrost" {
		t.Errorf("version = %+v", got)
	}
}
