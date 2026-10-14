package control_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/47vigen/remora/test/fakes/control"
)

func newServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(control.New(nil).Handler())
	t.Cleanup(srv.Close)
	return srv
}

func do(t *testing.T, method, url, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, string(b)
}

func putScript(t *testing.T, base, upstream, route string, s control.Script) {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	code, body := do(t, http.MethodPut, base+"/_control/upstreams/"+upstream+"/routes/"+route, string(b))
	if code != http.StatusNoContent {
		t.Fatalf("put script: status %d body %q", code, body)
	}
}

func requests(t *testing.T, base, upstream string) []control.RecordedRequest {
	t.Helper()
	code, body := do(t, http.MethodGet, base+"/_control/upstreams/"+upstream+"/requests", "")
	if code != http.StatusOK {
		t.Fatalf("get requests: status %d body %q", code, body)
	}
	var out []control.RecordedRequest
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode requests: %v (body %q)", err, body)
	}
	return out
}

func TestRouteResolution(t *testing.T) {
	srv := newServer(t)
	tests := []struct {
		method, path, route string
	}{
		{http.MethodPost, "/v1/chat/completions", control.RouteOpenAIChat},
		{http.MethodPost, "/v1/embeddings", control.RouteOpenAIEmbeddings},
		{http.MethodPost, "/v1/audio/transcriptions", control.RouteOpenAITranscriptions},
		{http.MethodPost, "/v1/decisions", control.RouteOpenAIDecisions},
		{http.MethodGet, "/v1/models", control.RouteModels},
		{http.MethodPost, "/v1/files", control.RouteSonioxFileCreate},
		{http.MethodGet, "/v1/files", control.RouteSonioxFileList},
		{http.MethodDelete, "/v1/files/f1", control.RouteSonioxFileDelete},
		{http.MethodPost, "/v1/transcriptions", control.RouteSonioxTxCreate},
		{http.MethodGet, "/v1/transcriptions", control.RouteSonioxTxList},
		{http.MethodGet, "/v1/transcriptions/t1", control.RouteSonioxTxGet},
		{http.MethodDelete, "/v1/transcriptions/t1", control.RouteSonioxTxDelete},
		{http.MethodGet, "/v1/transcriptions/t1/transcript", control.RouteSonioxTranscript},
		{http.MethodPost, "/v1/systemone", control.RouteTypeSafeSystemOne},
	}
	for _, tt := range tests {
		t.Run(tt.route, func(t *testing.T) {
			code, body := do(t, tt.method, srv.URL+"/u/x"+tt.path, "")
			if code != http.StatusNotImplemented {
				t.Fatalf("status = %d, want 501", code)
			}
			if !strings.Contains(body, `"fake: no default for `+tt.route+`"`) {
				t.Fatalf("body %q does not name route %q", body, tt.route)
			}
		})
	}
	t.Run("unknown path", func(t *testing.T) {
		code, _ := do(t, http.MethodPost, srv.URL+"/u/x/v1/nope", "")
		if code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", code)
		}
	})
}

func TestUnscriptedRouteReturns501(t *testing.T) {
	srv := newServer(t)
	code, body := do(t, http.MethodPost, srv.URL+"/u/a/v1/chat/completions", "{}")
	if code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", code)
	}
	if !strings.Contains(body, "openai.chat") {
		t.Fatalf("body %q does not contain openai.chat", body)
	}
}

func TestDefaultHandlerUsed(t *testing.T) {
	s := control.New(map[string]http.HandlerFunc{
		control.RouteModels: func(w http.ResponseWriter, r *http.Request) {
			//nolint:gosec // G705: test handler echoing a path value back to the test's own client; nothing renders it.
			_, _ = io.WriteString(w, "default for "+r.PathValue("upstream"))
		},
	})
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	code, body := do(t, http.MethodGet, srv.URL+"/u/a/v1/models", "")
	if code != http.StatusOK || body != "default for a" {
		t.Fatalf("got %d %q", code, body)
	}
}

func TestScriptedStepsInOrder(t *testing.T) {
	srv := newServer(t)
	errBody := `{"error":{"message":"slow down"}}`
	putScript(t, srv.URL, "a", control.RouteOpenAIChat, control.Script{Steps: []control.Step{
		{Status: 429, Body: errBody},
		{Status: 402},
	}})
	url := srv.URL + "/u/a/v1/chat/completions"
	code, body := do(t, http.MethodPost, url, "{}")
	if code != 429 || body != errBody {
		t.Fatalf("call 1: got %d %q", code, body)
	}
	if code, _ := do(t, http.MethodPost, url, "{}"); code != 402 {
		t.Fatalf("call 2: got %d, want 402", code)
	}
	if code, _ := do(t, http.MethodPost, url, "{}"); code != 501 {
		t.Fatalf("call 3: got %d, want 501", code)
	}
}

func TestStepDefaultsAndHeaders(t *testing.T) {
	srv := newServer(t)
	putScript(t, srv.URL, "a", control.RouteModels, control.Script{Steps: []control.Step{
		{Body: `{"data":[]}`, Headers: map[string]string{"X-Fake": "1"}},
	}})
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/u/a/v1/models", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("content-type = %q", got)
	}
	if got := resp.Header.Get("X-Fake"); got != "1" {
		t.Fatalf("X-Fake = %q", got)
	}
}

func TestScriptRepeat(t *testing.T) {
	srv := newServer(t)
	putScript(t, srv.URL, "a", control.RouteOpenAIEmbeddings, control.Script{
		Steps:  []control.Step{{Status: 503}},
		Repeat: true,
	})
	for i := range 5 {
		if code, _ := do(t, http.MethodPost, srv.URL+"/u/a/v1/embeddings", "{}"); code != 503 {
			t.Fatalf("call %d: got %d, want 503", i+1, code)
		}
	}
}

func TestScriptsAreIsolatedPerUpstream(t *testing.T) {
	srv := newServer(t)
	putScript(t, srv.URL, "a", control.RouteOpenAIChat, control.Script{
		Steps: []control.Step{{Status: 429}}, Repeat: true,
	})
	if code, _ := do(t, http.MethodPost, srv.URL+"/u/b/v1/chat/completions", "{}"); code != 501 {
		t.Fatalf("upstream b: got %d, want 501", code)
	}
	if code, _ := do(t, http.MethodPost, srv.URL+"/u/a/v1/chat/completions", "{}"); code != 429 {
		t.Fatalf("upstream a: got %d, want 429", code)
	}
	if got := requests(t, srv.URL, "b"); len(got) != 1 {
		t.Fatalf("upstream b recorded %d requests, want 1", len(got))
	}
}

func TestRequestsRecorded(t *testing.T) {
	srv := newServer(t)
	if got := requests(t, srv.URL, "a"); len(got) != 0 {
		t.Fatalf("fresh upstream recorded %d requests", len(got))
	}
	bodies := []string{
		`{"model":"m","messages":[{"role":"user","content":"hi"}]}`,
		"{\"input\":\"می\u200cخواهم\"}",
	}
	if !strings.Contains(bodies[1], "\u200c") {
		t.Fatal("fixture lost its zero-width non-joiner")
	}
	do(t, http.MethodPost, srv.URL+"/u/a/v1/chat/completions", bodies[0])
	do(t, http.MethodPost, srv.URL+"/u/a/v1/embeddings", bodies[1])

	got := requests(t, srv.URL, "a")
	if len(got) != 2 {
		t.Fatalf("recorded %d requests, want 2", len(got))
	}
	wantRoutes := []string{control.RouteOpenAIChat, control.RouteOpenAIEmbeddings}
	wantPaths := []string{"/u/a/v1/chat/completions", "/u/a/v1/embeddings"}
	for i, r := range got {
		if r.Route != wantRoutes[i] {
			t.Errorf("request %d route = %q, want %q", i, r.Route, wantRoutes[i])
		}
		if r.Method != http.MethodPost || r.Path != wantPaths[i] {
			t.Errorf("request %d = %s %s", i, r.Method, r.Path)
		}
		if !bytes.Equal(r.Body, []byte(bodies[i])) {
			t.Errorf("request %d body = %q, want %q", i, r.Body, bodies[i])
		}
		if r.At.IsZero() {
			t.Errorf("request %d has zero At", i)
		}
	}
}

// writeTracker records whether the handler wrote anything.
type writeTracker struct {
	header http.Header
	wrote  bool
}

func (w *writeTracker) Header() http.Header         { return w.header }
func (w *writeTracker) Write(b []byte) (int, error) { w.wrote = true; return len(b), nil }
func (w *writeTracker) WriteHeader(int)             { w.wrote = true }

func TestDelay(t *testing.T) {
	srv := newServer(t)
	putScript(t, srv.URL, "a", control.RouteModels, control.Script{
		Steps: []control.Step{{DelayMS: 150}}, Repeat: true,
	})
	start := time.Now()
	if code, _ := do(t, http.MethodGet, srv.URL+"/u/a/v1/models", ""); code != 200 {
		t.Fatalf("status = %d, want 200", code)
	}
	if elapsed := time.Since(start); elapsed < 150*time.Millisecond {
		t.Fatalf("elapsed %v, want >= 150ms", elapsed)
	}

	t.Run("canceled", func(t *testing.T) {
		s := control.New(nil)
		h := s.Handler()
		w := &writeTracker{header: http.Header{}}
		put := httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/_control/upstreams/a/routes/models",
			strings.NewReader(`{"steps":[{"delay_ms":1000}]}`))
		h.ServeHTTP(httptest.NewRecorder(), put)

		ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		defer cancel()
		req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/u/a/v1/models", nil)
		start := time.Now()
		h.ServeHTTP(w, req)
		elapsed := time.Since(start)
		if elapsed >= 900*time.Millisecond {
			t.Fatalf("handler took %v, should return on cancel", elapsed)
		}
		if w.wrote {
			t.Fatal("handler wrote a response after cancellation")
		}
		if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
			t.Fatalf("ctx err = %v", ctx.Err())
		}
	})
}

func TestReset(t *testing.T) {
	srv := newServer(t)
	putScript(t, srv.URL, "a", control.RouteOpenAIChat, control.Script{
		Steps: []control.Step{{Status: 429}}, Repeat: true,
	})
	do(t, http.MethodPost, srv.URL+"/u/a/v1/chat/completions", "{}")
	code, _ := do(t, http.MethodPost, srv.URL+"/_control/reset", "")
	if code != http.StatusNoContent {
		t.Fatalf("reset status = %d, want 204", code)
	}
	if got := requests(t, srv.URL, "a"); len(got) != 0 {
		t.Fatalf("after reset recorded %d requests, want 0", len(got))
	}
	if code, _ := do(t, http.MethodPost, srv.URL+"/u/a/v1/chat/completions", "{}"); code != 501 {
		t.Fatalf("after reset: got %d, want 501", code)
	}
}

func TestUnknownRouteInControl(t *testing.T) {
	srv := newServer(t)
	code, _ := do(t, http.MethodPut, srv.URL+"/_control/upstreams/a/routes/nope", `{"steps":[]}`)
	if code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", code)
	}
}

func TestBadScriptBody(t *testing.T) {
	srv := newServer(t)
	code, _ := do(t, http.MethodPut, srv.URL+"/_control/upstreams/a/routes/models", `{not json`)
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", code)
	}
}

func TestHealthz(t *testing.T) {
	srv := newServer(t)
	if code, _ := do(t, http.MethodGet, srv.URL+"/healthz", ""); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
}
