package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestRunMCPServesTools(t *testing.T) {
	var (
		mu   sync.Mutex
		auth []string
	)
	answers := map[string]string{
		"/api/health":         `{"status":"ok","engines":{}}`,
		"/api/system/version": `{"remora":"dev","engines":[]}`,
		"/api/accounts/pool":  `[]`,
		"/api/quota":          `[]`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		auth = append(auth, r.Header.Get("Authorization"))
		mu.Unlock()
		body, ok := answers[r.URL.Path]
		if !ok {
			body = `{"items":[],"total":0}`
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	defer srv.Close()

	inR, inW := io.Pipe()   // client → runMCP's stdin
	outR, outW := io.Pipe() // runMCP's stdout → client
	env := map[string]string{"REMORA_URL": srv.URL, "REMORA_TOKEN": "rmt_test"}
	errc := make(chan error, 1)
	go func() {
		errc <- runMCP(context.Background(), nil, inR, outW, func(k string) (string, bool) { v, ok := env[k]; return v, ok })
	}()

	cs, err := sdk.NewClient(&sdk.Implementation{Name: "t", Version: "0"}, nil).Connect(context.Background(), &sdk.IOTransport{Reader: outR, Writer: inW}, nil)
	if err != nil {
		t.Fatal(err)
	}
	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil || len(tools.Tools) != 11 {
		t.Fatalf("tools = %d, %v; want 11", len(tools.Tools), err)
	}
	res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: "remora_overview", Arguments: map[string]any{}})
	if err != nil || res.IsError {
		t.Fatalf("overview = %+v, %v", res, err)
	}
	mu.Lock()
	for _, a := range auth {
		if a != "Bearer rmt_test" {
			t.Errorf("the API saw Authorization %q", a)
		}
	}
	mu.Unlock()

	_ = cs.Close() // closes stdin
	select {
	case err := <-errc:
		if err != nil {
			t.Errorf("runMCP = %v, want nil when stdin closes", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runMCP did not return after stdin closed")
	}
}

func TestRunMCPNeedsToken(t *testing.T) {
	var out bytes.Buffer
	err := runMCP(context.Background(), nil, io.NopCloser(strings.NewReader("")), nopWriteCloser{&out}, func(string) (string, bool) { return "", false })
	if err == nil || !strings.Contains(err.Error(), "set REMORA_TOKEN") || out.Len() != 0 {
		t.Errorf("err %v, stdout %q", err, out.String())
	}
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }
