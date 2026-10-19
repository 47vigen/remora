package mcp

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/47vigen/remora/internal/api"
	"github.com/47vigen/remora/internal/auth"
	"github.com/47vigen/remora/internal/store"
)

const initialize = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`

type mcpEnv struct {
	svc  *auth.Service
	st   *store.Store
	srv  *httptest.Server
	mu   sync.Mutex
	seen []string // Authorization of each /api call
}

// newMCPEnv serves /mcp as serve.go does, over a real auth service and API handler on a temp store.
func newMCPEnv(t *testing.T) *mcpEnv {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "remora.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	e := &mcpEnv{svc: auth.NewService(st, nil), st: st}
	apiH := api.New(api.Deps{Auth: e.svc}).Handler()
	recording := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.mu.Lock()
		e.seen = append(e.seen, r.Header.Get("Authorization"))
		e.mu.Unlock()
		apiH.ServeHTTP(w, r)
	})
	c, err := InProcess(recording)
	if err != nil {
		t.Fatal(err)
	}
	e.srv = httptest.NewServer(Handler(New(c), e.svc))
	t.Cleanup(e.srv.Close)
	return e
}

func (e *mcpEnv) token(t *testing.T, name string) string {
	t.Helper()
	_, v, err := e.svc.CreateToken(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func (e *mcpEnv) post(t *testing.T, edit func(r *http.Request)) (int, string) {
	t.Helper()
	r, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, e.srv.URL, strings.NewReader(initialize))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json, text/event-stream")
	edit(r)
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestHandlerRequiresToken(t *testing.T) {
	e := newMCPEnv(t)
	code, body := e.post(t, func(*http.Request) {})
	if code != http.StatusUnauthorized || !strings.Contains(body, `"authentication_error"`) {
		t.Errorf("without a token = %d %s", code, body)
	}
	tok := e.token(t, "a")
	if code, body := e.post(t, func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+tok) }); code != http.StatusOK {
		t.Errorf("with a token = %d %s", code, body)
	}
}

func TestHandlerAcceptsLowercaseBearer(t *testing.T) {
	e := newMCPEnv(t)
	tok := e.token(t, "a")
	if code, body := e.post(t, func(r *http.Request) { r.Header.Set("Authorization", "bearer "+tok) }); code != http.StatusOK {
		t.Errorf("lowercase scheme = %d %s", code, body)
	}
}

func TestHandlerRefusesCookie(t *testing.T) {
	e := newMCPEnv(t)
	if _, err := auth.EnsureAdmin(context.Background(), e.st, "a-long-admin-password"); err != nil {
		t.Fatal(err)
	}
	session, _, err := e.svc.Login(context.Background(), "127.0.0.1", "a-long-admin-password")
	if err != nil {
		t.Fatal(err)
	}
	code, body := e.post(t, func(r *http.Request) { r.Header.Set("Cookie", auth.CookieName+"="+session) })
	if code != http.StatusUnauthorized {
		t.Errorf("session cookie only = %d %s", code, body)
	}
}

func TestHandlerBehindLocalProxy(t *testing.T) {
	e := newMCPEnv(t)
	tok := e.token(t, "a")
	code, body := e.post(t, func(r *http.Request) {
		r.Host = "remora.example"
		r.Header.Set("Authorization", "Bearer "+tok)
	})
	if code != http.StatusOK {
		t.Errorf("behind a local proxy = %d %s", code, body)
	}
}

func TestToolCallUsesCallerToken(t *testing.T) {
	e := newMCPEnv(t)
	e.token(t, "a")
	b := e.token(t, "b")
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "t", Version: "0"}, nil).Connect(context.Background(), &sdk.StreamableClientTransport{
		Endpoint: e.srv.URL, DisableStandaloneSSE: true, HTTPClient: &http.Client{Transport: bearerTransport(b)},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cs.Close() }()
	call(t, cs, "remora_overview", map[string]any{})
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.seen) == 0 || slices.ContainsFunc(e.seen, func(v string) bool { return v != "Bearer "+b }) {
		t.Errorf("the API saw %q, want every call with token b", e.seen)
	}
}

type bearerTransport string

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+string(b))
	return http.DefaultTransport.RoundTrip(r)
}

func TestListenStreamSendsHeartbeats(t *testing.T) {
	e := newMCPEnv(t)
	tok := e.token(t, "a")
	defer func(d time.Duration) { listenHeartbeat = d }(listenHeartbeat)
	listenHeartbeat = 10 * time.Millisecond
	for _, withHeader := range []bool{true, false} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		r, _ := http.NewRequestWithContext(ctx, http.MethodPost, e.srv.URL,
			strings.NewReader(`{"jsonrpc":"2.0","id":"listen:0","method":"subscriptions/listen","params":{}}`))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "application/json, text/event-stream")
		r.Header.Set("Authorization", "Bearer "+tok)
		if withHeader {
			r.Header.Set("Mcp-Method", "subscriptions/listen")
		}
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/event-stream" {
			t.Errorf("header %v: listen = %d %s", withHeader, resp.StatusCode, resp.Header.Get("Content-Type"))
		}
		buf := make([]byte, 64)
		got := ""
		for strings.Count(got, ": keepalive\n\n") < 2 {
			n, err := resp.Body.Read(buf)
			if err != nil {
				t.Fatalf("header %v: after %q: %v", withHeader, got, err)
			}
			got += string(buf[:n])
		}
		cancel()
		_ = resp.Body.Close()
	}
}
