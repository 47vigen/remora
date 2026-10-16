package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/47vigen/remora/internal/api/gen"
)

// recorded is one request the fake API saw.
type recorded struct {
	Method, Path, Query, Body, Authorization string
}

// fakeAPI is a management API with a canned answer per "METHOD /api/path"; anything else is 404.
type fakeAPI struct {
	srv     *httptest.Server
	mu      sync.Mutex
	answers map[string]answer
	reqs    []recorded
}

type answer struct {
	status      int
	contentType string
	body        string
}

func newFakeAPI(t *testing.T) *fakeAPI {
	t.Helper()
	f := &fakeAPI{answers: map[string]answer{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.reqs = append(f.reqs, recorded{r.Method, r.URL.Path, r.URL.RawQuery, string(b), r.Header.Get("Authorization")})
		a, ok := f.answers[r.Method+" "+r.URL.Path]
		f.mu.Unlock()
		if !ok {
			a = answer{http.StatusNotFound, "application/json", `{"error":{"message":"Unknown path or method.","type":"not_found_error","code":"not_found"}}`}
		}
		if a.body != "" {
			w.Header().Set("Content-Type", a.contentType)
		}
		w.WriteHeader(a.status)
		_, _ = io.WriteString(w, a.body)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAPI) on(pattern string, status int, body string) {
	f.onType(pattern, status, "application/json", body)
}

func (f *fakeAPI) onType(pattern string, status int, contentType, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answers[pattern] = answer{status, contentType, body}
}

func (f *fakeAPI) requests() []recorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recorded(nil), f.reqs...)
}

// session connects New over the fake API to an MCP client in memory.
func (f *fakeAPI) session(t *testing.T) *sdk.ClientSession {
	t.Helper()
	c, err := gen.NewClientWithResponses(f.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return connect(t, New(c))
}

func connect(t *testing.T, s *sdk.Server) *sdk.ClientSession {
	t.Helper()
	ctx := context.Background()
	st, ct := sdk.NewInMemoryTransports()
	ss, err := s.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// call calls a tool and returns its result and decoded structuredContent.
func call(t *testing.T, cs *sdk.ClientSession, name string, args any) (*sdk.CallToolResult, map[string]any) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	var out map[string]any
	if res.StructuredContent != nil {
		b, _ := json.Marshal(res.StructuredContent)
		if err := json.Unmarshal(b, &out); err != nil {
			t.Fatalf("structuredContent %s: %v", b, err)
		}
	}
	return res, out
}

// text is a result's first text content.
func text(res *sdk.CallToolResult) string {
	for _, c := range res.Content {
		if t, ok := c.(*sdk.TextContent); ok {
			return t.Text
		}
	}
	return ""
}
