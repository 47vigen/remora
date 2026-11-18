package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/47vigen/remora/internal/accounts"
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
	RegisterHealthz(mux, src, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), method, path, http.NoBody))
	return rec
}

func checkHealthz(t *testing.T, src StatusSource, wantCode int, wantBody string) {
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

func TestHealthzDegradesAfterUnhealthyProbe(t *testing.T) {
	bin, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("sleep executable not available")
	}
	data, err := os.ReadFile(bin) // #nosec G304 -- test hashes the executable it just looked up
	if err != nil {
		t.Fatal(err)
	}
	sha := sha256.Sum256(data)
	var unhealthy atomic.Bool
	var probes atomic.Int32
	health := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// A successful probe between two groups of failures must reset the count.
		if unhealthy.Load() && probes.Add(1) != 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(health.Close)

	sup := supervise.New(nil, map[string]supervise.EngineVersion{
		"bifrost": {Version: "test", SHA256: hex.EncodeToString(sha[:])},
	}, supervise.Options{HealthInterval: 50 * time.Millisecond, BackoffMin: time.Second})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := sup.Stop(ctx); err != nil {
			t.Error(err)
		}
	})
	if err := sup.Start(t.Context(), supervise.Engine{
		Name: "bifrost", Bin: bin, Args: []string{"60"}, HealthURL: health.URL,
	}); err != nil {
		t.Fatal(err)
	}
	checkHealthz(t, sup, http.StatusOK, `{"status":"ok"}`)

	unhealthy.Store(true)
	deadline := time.Now().Add(time.Second)
	for sup.Status()[0].Up && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	checkHealthz(t, sup, http.StatusServiceUnavailable, `{"status":"degraded","down":["router"]}`)
	if got := probes.Load(); got != 6 {
		t.Errorf("engine went down after %d probes, want six (fail, fail, pass, fail, fail, fail)", got)
	}
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
	if got.Remora != version.Version || len(got.Engines) != 0 {
		t.Errorf("version = %+v", got)
	}
}

type fakePool struct {
	pool []accounts.PoolStatus
	err  error
}

func (f fakePool) Pool(context.Context) ([]accounts.PoolStatus, error) { return f.pool, f.err }

var twoOut = fakePool{pool: []accounts.PoolStatus{{Provider: "antigravity", Enabled: 20, Out: []accounts.OutAccount{
	{ID: 3, Label: "acct-three", Reason: "signed out"}, {ID: 7, Label: "acct-seven", Reason: "missing"},
}}}}

func TestHealthzReportsAccountsOut(t *testing.T) {
	healthz := func(pool PoolSource) *httptest.ResponseRecorder {
		mux := http.NewServeMux()
		RegisterHealthz(mux, both(true), pool)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/healthz", http.NoBody))
		return rec
	}
	rec := healthz(twoOut)
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"status":"ok","accounts_out":2}` {
		t.Fatalf("code %d body %s", rec.Code, rec.Body)
	}
	for _, s := range []string{"antigravity", "acct-three", "acct-seven"} {
		if strings.Contains(rec.Body.String(), s) {
			t.Errorf("public /healthz names %q", s)
		}
	}
	rec = healthz(fakePool{err: errors.New("engine down")})
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"status":"ok"}` {
		t.Fatalf("pool error: code %d body %s", rec.Code, rec.Body)
	}
}

func TestVersionAuthentication(t *testing.T) {
	env := newTestEnv(t)
	for _, tc := range []struct {
		name    string
		client  *http.Client
		engines bool
	}{
		{"anonymous", http.DefaultClient, false}, {"authenticated", env.client, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, body := env.do(t, tc.client, http.MethodGet, "/api/system/version", "")
			var got map[string]json.RawMessage
			if code != http.StatusOK {
				t.Fatalf("status = %d", code)
			}
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatal(err)
			}
			if string(got["remora"]) != `"v0.0.0-test"` {
				t.Errorf("remora = %s", got["remora"])
			}
			engines, present := got["engines"]
			if present != tc.engines {
				t.Errorf("engines present = %v", present)
			}
			if tc.engines {
				var entries []EngineVersion
				if err := json.Unmarshal(engines, &entries); err != nil || len(entries) != 2 {
					t.Errorf("engines = %s, err = %v", engines, err)
				}
			}
		})
	}
}
