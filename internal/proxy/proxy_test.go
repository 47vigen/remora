package proxy

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/47vigen/remora/internal/alias"
	"github.com/47vigen/remora/internal/store"
)

const (
	keyFull     = "sk-e2e-full"
	keyLimited  = "sk-e2e-limited"
	keyDisabled = "sk-e2e-disabled"
	keyNoVK     = "sk-e2e-novk"
	keyNarrow   = "sk-e2e-narrow"

	vkFull    = "sk-bf-vk-full"
	vkLimited = "sk-bf-vk-limited"
)

// seen is what the fake front door recorded about one request.
type seen struct {
	method, path string
	header       http.Header
	body         []byte
}

// fixture is a real store, a fake front door and the proxy in front of it.
type fixture struct {
	st      *store.Store
	dbPath  string
	px      *Proxy
	srv     *httptest.Server
	hits    atomic.Int32
	mu      sync.Mutex
	last    seen
	handler atomic.Pointer[http.HandlerFunc]
}

func okJSON(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"id":"x","object":"chat.completion"}`)
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "remora.db")
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	sl, err := store.NewSealer(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatalf("NewSealer: %v", err)
	}
	st.SetSealer(sl)

	err = st.Write(ctx, func(tx *store.Tx) error {
		keys := []struct {
			name, value, vk string
			allow           []string
			enabled         bool
		}{
			{"full", keyFull, vkFull, []string{"*"}, true},
			{"limited", keyLimited, vkLimited, []string{"fast", "up-a/*"}, true},
			{"disabled", keyDisabled, "sk-bf-vk-disabled", []string{"*"}, false},
			{"novk", keyNoVK, "", []string{"*"}, true},
			{"narrow", keyNarrow, "sk-bf-vk-narrow", []string{"up-a/*"}, true},
		}
		for _, k := range keys {
			ck, err := tx.PutClientKey(ctx, store.ClientKey{Name: k.name, Allow: k.allow, Enabled: k.enabled}, k.value)
			if err != nil {
				return err
			}
			if k.vk != "" {
				if err := tx.SetClientKeyVK(ctx, ck.ID, "vk-"+k.name, k.vk); err != nil {
					return err
				}
			}
		}
		aliases := []alias.Alias{
			{
				Name: "fast", Kind: alias.KindChat, Members: []alias.Member{{Ref: "up-a/m1"}, {Ref: "up-b/m1"}},
				Capabilities: []string{"structured_outputs", "tools"}, ContextWindow: 131072,
			},
			{Name: "memory/fast", Kind: alias.KindChat, Members: []alias.Member{{Ref: "fast"}}},
			{Name: "emb", Kind: alias.KindEmbeddings, Members: []alias.Member{{Ref: "up-a/e1"}}},
		}
		for _, a := range aliases {
			if err := tx.PutAlias(ctx, a); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed store: %v", err)
	}

	f := &fixture{st: st, dbPath: dbPath}
	h := http.HandlerFunc(okJSON)
	f.handler.Store(&h)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hits.Add(1)
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.last = seen{method: r.Method, path: r.URL.Path, header: r.Header.Clone(), body: body}
		f.mu.Unlock()
		r.Body = io.NopCloser(bytes.NewReader(body))
		(*f.handler.Load())(w, r)
	}))
	t.Cleanup(up.Close)
	u, err := url.Parse(up.URL)
	if err != nil {
		t.Fatal(err)
	}
	f.px = New(u, st, slog.New(slog.DiscardHandler))
	f.srv = httptest.NewServer(f.px)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fixture) upstream(h http.HandlerFunc) { f.handler.Store(&h) }

func (f *fixture) seen() seen {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.last
}

// result is a response read to the end.
type result struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

// do sends a request to the proxy and reads the whole answer. key, when set, is sent as a Bearer token; header
// holds extra request headers.
func (f *fixture) do(t *testing.T, method, path, key, contentType string, body []byte, header map[string]string) result {
	t.Helper()
	resp := f.open(t, method, path, key, contentType, body, header)
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return result{StatusCode: resp.StatusCode, Header: resp.Header, Body: b}
}

// open sends a request to the proxy and returns the response unread; the caller closes its body.
func (f *fixture) open(t *testing.T, method, path, key, contentType string, body []byte, header map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, f.srv.URL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

func chatBody(model string) []byte {
	return []byte(`{"model":"` + model + `","messages":[{"role":"user","content":"hi"}]}`)
}

// errCode returns error.code of an OpenAI-shaped error body.
func errCode(t *testing.T, body []byte) string {
	t.Helper()
	var e struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &e); err != nil {
		t.Fatalf("error body %q: %v", body, err)
	}
	return e.Error.Code
}

func TestProxy_Auth(t *testing.T) {
	f := newFixture(t)
	cases := []struct {
		name   string
		header map[string]string
		want   int
	}{
		{"no key", nil, http.StatusUnauthorized},
		{"wrong bearer", map[string]string{"Authorization": "Bearer wrong"}, http.StatusUnauthorized},
		{"disabled key", map[string]string{"Authorization": "Bearer " + keyDisabled}, http.StatusUnauthorized},
		{"not bearer", map[string]string{"Authorization": "Basic " + keyFull}, http.StatusUnauthorized},
		{"x-api-key", map[string]string{"x-api-key": keyFull}, http.StatusOK},
		{"bearer", map[string]string{"Authorization": "Bearer " + keyFull}, http.StatusOK},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			before := f.hits.Load()
			resp := f.do(t, http.MethodPost, "/v1/chat/completions", "", "application/json", chatBody("fast"), c.header)
			body := resp.Body
			if resp.StatusCode != c.want {
				t.Fatalf("status = %d, want %d (%s)", resp.StatusCode, c.want, body)
			}
			hit := f.hits.Load() - before
			if c.want == http.StatusOK && hit != 1 {
				t.Fatalf("upstream hits = %d, want 1", hit)
			}
			if c.want != http.StatusOK {
				if hit != 0 {
					t.Fatalf("upstream hits = %d, want 0", hit)
				}
				if code := errCode(t, body); code != "invalid_api_key" {
					t.Fatalf("code = %q, want invalid_api_key", code)
				}
			}
		})
	}
}

func TestProxy_RequestErrors(t *testing.T) {
	f := newFixture(t)
	cases := []struct {
		name, key, body string
		status          int
		code            string
	}{
		{"no model", keyFull, `{"messages":[]}`, http.StatusBadRequest, "model_required"},
		{"client fallbacks", keyFull, `{"model":"fast","fallbacks":["up-b/m1"]}`, http.StatusBadRequest, "fallbacks_not_supported"},
		{"no virtual key yet", keyNoVK, `{"model":"fast"}`, http.StatusServiceUnavailable, "key_not_ready"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := f.do(t, http.MethodPost, "/v1/chat/completions", c.key, "application/json", []byte(c.body), nil)
			body := resp.Body
			if resp.StatusCode != c.status || errCode(t, body) != c.code {
				t.Fatalf("got %d %s, want %d %s", resp.StatusCode, body, c.status, c.code)
			}
		})
	}
	t.Run("too large", func(t *testing.T) {
		big := bytes.Repeat([]byte("a"), MaxBodyBytes+1)
		resp := f.do(t, http.MethodPost, "/v1/chat/completions", keyFull, "application/json", big, nil)
		if resp.StatusCode != http.StatusRequestEntityTooLarge {
			t.Fatalf("status = %d, want 413", resp.StatusCode)
		}
	})
	if n := f.hits.Load(); n != 0 {
		t.Fatalf("upstream hits = %d, want 0", n)
	}
}

func TestProxy_Routes(t *testing.T) {
	f := newFixture(t)
	cases := []struct{ path, upstream string }{
		{"/v1/chat/completions", "/v1/chat/completions"},
		{"/v1/responses", "/v1/responses"},
		{"/v1/messages", "/anthropic/v1/messages"},
		{"/v1/embeddings", "/v1/embeddings"},
		{"/v1/decisions", "/v1/decisions"},
		{"/v1/systemone", "/typesafe/v1/systemone"},
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			resp := f.do(t, http.MethodPost, c.path, keyFull, "application/json", chatBody("up-a/m1"), nil)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d", resp.StatusCode)
			}
			if got := f.seen(); got.path != c.upstream || got.method != http.MethodPost {
				t.Fatalf("upstream saw %s %s, want POST %s", got.method, got.path, c.upstream)
			}
		})
	}
	t.Run("/v1/audio/transcriptions", func(t *testing.T) {
		body, ct := multipartBody(t, []field{{name: "file", filename: "a.wav", value: []byte("RIFF")}, {name: "model", value: []byte("up-a/m1")}})
		resp := f.do(t, http.MethodPost, "/v1/audio/transcriptions", keyFull, ct, body, nil)
		if resp.StatusCode != http.StatusOK || f.seen().path != "/v1/audio/transcriptions" {
			t.Fatalf("status = %d, upstream path %q", resp.StatusCode, f.seen().path)
		}
	})
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/v1/nope"},
		{http.MethodPost, "/v1/nope"},
		{http.MethodGet, "/v1/chat/completions"},
		{http.MethodPost, "/v1/chat/completions/extra"},
	} {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			before := f.hits.Load()
			resp := f.do(t, c.method, c.path, keyFull, "application/json", chatBody("fast"), nil)
			body := resp.Body
			if resp.StatusCode != http.StatusNotFound || errCode(t, body) != "not_found" {
				t.Fatalf("got %d %s, want 404 not_found", resp.StatusCode, body)
			}
			if f.hits.Load() != before {
				t.Fatal("upstream was hit")
			}
		})
	}
}

func TestProxy_Allowlist(t *testing.T) {
	f := newFixture(t)
	for _, c := range []struct {
		model string
		want  int
	}{
		{"fast", http.StatusOK},
		{"up-a/m1", http.StatusOK},
		{"memory/fast", http.StatusForbidden},
		{"up-b/m1", http.StatusForbidden},
	} {
		t.Run(c.model, func(t *testing.T) {
			before := f.hits.Load()
			resp := f.do(t, http.MethodPost, "/v1/chat/completions", keyLimited, "application/json", chatBody(c.model), nil)
			body := resp.Body
			if resp.StatusCode != c.want {
				t.Fatalf("status = %d, want %d (%s)", resp.StatusCode, c.want, body)
			}
			if c.want == http.StatusForbidden {
				if errCode(t, body) != "model_not_allowed" || !strings.Contains(string(body), c.model) {
					t.Fatalf("body = %s", body)
				}
				if f.hits.Load() != before {
					t.Fatal("upstream was hit on 403")
				}
			}
		})
	}
	t.Run("anthropic shape", func(t *testing.T) {
		resp := f.do(t, http.MethodPost, "/v1/messages", keyLimited, "application/json", chatBody("up-b/m1"), nil)
		var e struct {
			Type  string `json:"type"`
			Error struct{ Type, Message string }
		}
		if err := json.Unmarshal(resp.Body, &e); err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusForbidden || e.Type != "error" || e.Error.Type != "permission_error" ||
			!strings.Contains(e.Error.Message, "up-b/m1") {
			t.Fatalf("got %d %+v", resp.StatusCode, e)
		}
	})
	t.Run("typesafe shape", func(t *testing.T) {
		resp := f.do(t, http.MethodPost, "/v1/systemone", keyLimited, "application/json", chatBody("up-b/m1"), nil)
		var e struct {
			Detail struct {
				ErrorType string `json:"error_type"`
				Message   string `json:"message"`
			} `json:"detail"`
		}
		if err := json.Unmarshal(resp.Body, &e); err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusForbidden || e.Detail.ErrorType != "permission_error" || e.Detail.Message == "" {
			t.Fatalf("got %d %+v", resp.StatusCode, e)
		}
	})
}

func TestProxy_StripsClientEngineHeaders(t *testing.T) {
	f := newFixture(t)
	resp := f.do(t, http.MethodPost, "/v1/chat/completions", keyFull, "application/json", chatBody("fast"), map[string]string{
		"x-bf-vk":                         "sk-bf-evil",
		"x-bf-compat":                     "true",
		"x-bf-passthrough-extra-params":   "false",
		"X-Bifrost-Anything":              "1",
		"Cookie":                          "a=b",
		"x-api-key":                       keyLimited,
		"Accept-Encoding":                 "gzip",
		"X-Custom-Header":                 "kept",
		"x-bifrost-routing-info-provider": "evil",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	h := f.seen().header
	if got := h.Values("X-Bf-Vk"); len(got) != 1 || got[0] != vkFull {
		t.Fatalf("x-bf-vk = %q, want [%q]", got, vkFull)
	}
	if got := h.Values("X-Bf-Passthrough-Extra-Params"); len(got) != 1 || got[0] != "true" {
		t.Fatalf("x-bf-passthrough-extra-params = %q", got)
	}
	for name := range h {
		l := strings.ToLower(name)
		switch {
		case l == "authorization", l == "cookie", l == "x-api-key", l == "x-bf-compat",
			strings.HasPrefix(l, "x-bifrost-"):
			t.Errorf("upstream saw client header %s: %q", name, h.Values(name))
		}
	}
	if h.Get("X-Custom-Header") != "kept" {
		t.Error("an ordinary client header was dropped")
	}
	if strings.Contains(h.Get("Accept-Encoding"), "gzip") {
		t.Errorf("Accept-Encoding = %q reached upstream", h.Get("Accept-Encoding"))
	}
}

func TestProxy_BodyUnchanged(t *testing.T) {
	f := newFixture(t)
	t.Run("persian json", func(t *testing.T) {
		body := []byte("{\"model\":\"fast\",\"messages\":[{\"role\":\"user\",\"content\":\"\u0645\u06cc\u200c\u0634\u0648\u062f \"}],\"x\":1}")
		resp := f.do(t, http.MethodPost, "/v1/chat/completions", keyFull, "application/json", body, nil)
		if resp.StatusCode != http.StatusOK || !bytes.Equal(f.seen().body, body) {
			t.Fatalf("status %d, upstream body %q, want %q", resp.StatusCode, f.seen().body, body)
		}
	})
	t.Run("multipart", func(t *testing.T) {
		body, ct := multipartBody(t, []field{
			{name: "file", filename: "a.wav", value: bytes.Repeat([]byte{0, 1, 2, 0xff}, 1<<16)},
			{name: "model", value: []byte("up-a/m1")},
		})
		resp := f.do(t, http.MethodPost, "/v1/audio/transcriptions", keyFull, ct, body, nil)
		got := f.seen()
		if resp.StatusCode != http.StatusOK || !bytes.Equal(got.body, body) || got.header.Get("Content-Type") != ct {
			t.Fatalf("status %d, body equal %v, content type %q", resp.StatusCode, bytes.Equal(got.body, body), got.header.Get("Content-Type"))
		}
	})
}

func TestProxy_StreamsUnbuffered(t *testing.T) {
	f := newFixture(t)
	chunk1 := "data: {\"choices\":[{\"delta\":{\"content\":\"\u0645\u06cc\u200c\u0634\u0648\u062f\"}}]}\n\n"
	chunk2 := "data: {\"choices\":[{\"delta\":{\"content\":\"bifrost is a word in content\"}}]}\n\n"
	done := "data: [DONE]\n\n"
	release := make(chan struct{})
	f.upstream(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, chunk1)
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-time.After(10 * time.Second):
			return
		}
		_, _ = io.WriteString(w, chunk2+done)
	})
	resp := f.open(t, http.MethodPost, "/v1/chat/completions", keyFull, "application/json", chatBody("fast"), nil)
	defer func() { _ = resp.Body.Close() }()
	br := bufio.NewReader(resp.Body)
	got := make([]byte, len(chunk1))
	readDone := make(chan error, 1)
	go func() { _, err := io.ReadFull(br, got); readDone <- err }()
	select {
	case err := <-readDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("first chunk was not flushed before the upstream wrote the rest")
	}
	close(release)
	rest, err := io.ReadAll(br)
	if err != nil {
		t.Fatal(err)
	}
	if all := string(got) + string(rest); all != chunk1+chunk2+done {
		t.Fatalf("client bytes = %q", all)
	}
}

func TestProxy_ResponseHeaders(t *testing.T) {
	f := newFixture(t)
	f.upstream(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("x-bifrost-routing-info-provider", "up-b")
		w.Header().Set("x-bifrost-routing-info-model", "m1")
		w.Header().Set("x-bifrost-routing-info-is-fallback", "true")
		w.Header().Set("x-bifrost-request-id", "r")
		w.Header().Set("x-bf-other", "1")
		w.Header().Set("Server", "Bifrost")
		w.Header().Set("X-Provider-Note", "via bifrost key sk-bf-abc")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Bf-Setup-Token")
		okJSON(w, r)
	})
	for _, c := range []struct{ model, alias string }{{"fast", "fast"}, {"memory/fast", "memory/fast"}, {"up-a/m1", ""}} {
		t.Run(c.model, func(t *testing.T) {
			resp := f.do(t, http.MethodPost, "/v1/chat/completions", keyFull, "application/json", chatBody(c.model), nil)
			h := resp.Header
			if got := h.Get("x-remora-alias"); got != c.alias {
				t.Errorf("x-remora-alias = %q, want %q", got, c.alias)
			}
			if _, ok := h[http.CanonicalHeaderKey("x-remora-alias")]; c.alias == "" && ok {
				t.Error("x-remora-alias set for a provider model")
			}
			if got := h.Get("x-remora-served-by"); got != "up-b/m1" {
				t.Errorf("x-remora-served-by = %q", got)
			}
			if got := h.Get("x-remora-fallback"); got != "true" {
				t.Errorf("x-remora-fallback = %q", got)
			}
			for name := range h {
				if strings.HasPrefix(strings.ToLower(name), "access-control-") {
					t.Errorf("client saw the front door's CORS header %s", name)
				}
			}
			for name, vals := range h {
				all := strings.ToLower(name + ":" + strings.Join(vals, ","))
				if strings.Contains(all, "bifrost") || strings.Contains(all, "sk-bf-") || strings.HasPrefix(strings.ToLower(name), "x-bf-") {
					t.Errorf("client saw engine header %s: %q", name, vals)
				}
			}
		})
	}
	t.Run("not a fallback", func(t *testing.T) {
		f.upstream(okJSON)
		resp := f.do(t, http.MethodPost, "/v1/chat/completions", keyFull, "application/json", chatBody("fast"), nil)
		if got := resp.Header.Get("x-remora-fallback"); got != "false" {
			t.Errorf("x-remora-fallback = %q, want false", got)
		}
		if _, ok := resp.Header[http.CanonicalHeaderKey("x-remora-served-by")]; ok {
			t.Error("x-remora-served-by set without routing info")
		}
	})
}

func TestProxy_ErrorMapping(t *testing.T) {
	f := newFixture(t)
	cases := []struct {
		name, path, upstream, want string
		status                     int
	}{
		{
			name: "native envelope", path: "/v1/chat/completions", status: http.StatusTooManyRequests,
			upstream: `{"is_bifrost_error":true,"status_code":429,"error":{"type":"rate_limit","code":"insufficient_quota","message":"bifrost: all fallbacks failed, key sk-bf-abc"},"extra_fields":{}}`,
			want:     `{"error":{"message":"remora: all fallbacks failed, key [redacted]","type":"rate_limit","code":"insufficient_quota","param":null}}`,
		},
		{
			name: "native envelope without type and code", path: "/v1/embeddings", status: http.StatusBadGateway,
			upstream: `{"is_bifrost_error":false,"error":{"message":"upstream <said> & failed","param":"model"}}`,
			want:     `{"error":{"message":"upstream <said> & failed","type":null,"code":null,"param":"model"}}`,
		},
		{
			name: "native envelope on messages", path: "/v1/messages", status: http.StatusTooManyRequests,
			upstream: `{"is_bifrost_error":true,"status_code":429,"error":{"type":"rate_limit_error","code":"insufficient_quota","message":"bifrost: key sk-bf-abc"},"extra_fields":{}}`,
			want:     `{"type":"error","error":{"type":"rate_limit_error","message":"remora: key [redacted]"}}`,
		},
		{
			name: "native envelope without type on messages", path: "/v1/messages", status: http.StatusBadGateway,
			upstream: `{"is_bifrost_error":true,"error":{"message":"upstream <said> & failed"}}`,
			want:     `{"type":"error","error":{"type":"api_error","message":"upstream <said> & failed"}}`,
		},
		{
			name: "native envelope on systemone", path: "/v1/systemone", status: http.StatusBadRequest,
			upstream: `{"is_bifrost_error":true,"status_code":400,"error":{"type":"invalid_request_error","message":"Bifrost rejected it"}}`,
			want:     `{"detail":{"error_type":"invalid_request_error","message":"remora rejected it"}}`,
		},
		{
			name: "native envelope without type on systemone", path: "/v1/systemone", status: http.StatusInternalServerError,
			upstream: `{"is_bifrost_error":true,"error":{"code":"x"}}`,
			want:     `{"detail":{"error_type":"api_error","message":"The upstream request failed."}}`,
		},
		{
			name: "anthropic passthrough", path: "/v1/messages", status: http.StatusBadRequest,
			upstream: `{"type":"error","error":{"type":"invalid_request_error","message":"bad"}}`,
			want:     `{"type":"error","error":{"type":"invalid_request_error","message":"bad"}}`,
		},
		{
			name: "typesafe passthrough scrubbed", path: "/v1/systemone", status: http.StatusBadRequest,
			upstream: `{"detail":{"error_type":"invalid","message":"BiFrost could not use sk-bf-x_y-1 or CLIProxyAPI"}}`,
			want:     `{"detail":{"error_type":"invalid","message":"remora could not use [redacted] or remora"}}`,
		},
		{
			name: "plain text scrubbed", path: "/v1/chat/completions", status: http.StatusInternalServerError,
			upstream: `internal bifrost failure`,
			want:     `internal remora failure`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f.upstream(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(c.status)
				_, _ = io.WriteString(w, c.upstream)
			})
			resp := f.do(t, http.MethodPost, c.path, keyFull, "application/json", chatBody("up-a/m1"), nil)
			body := resp.Body
			if resp.StatusCode != c.status || string(body) != c.want {
				t.Fatalf("got %d %s\nwant %d %s", resp.StatusCode, body, c.status, c.want)
			}
			if cl := resp.Header.Get("Content-Length"); cl != "" && cl != "0" && cl != itoa(len(body)) {
				t.Fatalf("Content-Length = %s, body is %d bytes", cl, len(body))
			}
		})
	}
	t.Run("oversized error body is replaced", func(t *testing.T) {
		big := strings.Repeat("x", 2<<20) + " bifrost sk-bf-abc"
		f.upstream(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, big)
		})
		resp := f.do(t, http.MethodPost, "/v1/chat/completions", keyFull, "application/json", chatBody("fast"), nil)
		body := resp.Body
		if resp.StatusCode != http.StatusBadRequest || errCode(t, body) != "upstream_error" ||
			strings.Contains(string(body), "bifrost") {
			t.Fatalf("got %d %.200s", resp.StatusCode, body)
		}
	})
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func TestProxy_SSEErrorEvent(t *testing.T) {
	f := newFixture(t)
	first := "data: {\"choices\":[{\"delta\":{\"content\":\"Bifrost \u200c\"}}]}\n\n"
	cases := []struct{ name, path, event, want string }{
		{
			name:  "native envelope on messages",
			path:  "/v1/messages",
			event: "event: error\ndata: {\"error\":{\"is_bifrost_error\":true,\"error\":{\"type\":\"overloaded_error\",\"message\":\"bifrost busy\"}}}\n\n",
			want:  "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"remora busy\"}}\n\n",
		},
		{
			name:  "top-level envelope",
			event: "data: {\"is_bifrost_error\":true,\"error\":{\"message\":\"Bifrost x\"}}\n\n",
			want:  "data: {\"error\":{\"message\":\"remora x\",\"type\":null,\"code\":null,\"param\":null}}\n\n",
		},
		{
			name:  "nested envelope",
			event: "data: {\"error\":{\"is_bifrost_error\":true,\"status_code\":500,\"error\":{\"type\":\"server_error\",\"message\":\"bifrost down, vk sk-bf-zz\"}}}\r\n\r\n",
			want:  "data: {\"error\":{\"message\":\"remora down, vk [redacted]\",\"type\":\"server_error\",\"code\":null,\"param\":null}}\r\n\r\n",
		},
		{
			name:  "anthropic error event",
			event: "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"api_error\",\"message\":\"bifrost overloaded\"}}\n\n",
			want:  "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"api_error\",\"message\":\"remora overloaded\"}}\n\n",
		},
		{
			name:  "plain error event",
			event: "data: {\"error\":\"bifrost plugin failed\"}\n\n",
			want:  "data: {\"error\":\"remora plugin failed\"}\n\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f.upstream(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
				_, _ = io.WriteString(w, first)
				w.(http.Flusher).Flush()
				_, _ = io.WriteString(w, c.event)
			})
			path := c.path
			if path == "" {
				path = "/v1/chat/completions"
			}
			resp := f.do(t, http.MethodPost, path, keyFull, "application/json", chatBody("fast"), nil)
			body := string(resp.Body)
			if body != first+c.want {
				t.Fatalf("stream = %q\nwant     %q", body, first+c.want)
			}
		})
	}
}

func TestProxy_SSELongLine(t *testing.T) {
	f := newFixture(t)
	long := "data: {\"choices\":[{\"delta\":{\"content\":\"" + strings.Repeat("\u0645", 200<<10) + "\"}}]}\n\n"
	f.upstream(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, long+"data: [DONE]\n\n")
	})
	resp := f.do(t, http.MethodPost, "/v1/chat/completions", keyFull, "application/json", chatBody("fast"), nil)
	if body := string(resp.Body); body != long+"data: [DONE]\n\n" {
		t.Fatalf("long line changed: got %d bytes", len(body))
	}
}

func TestProxy_ClientCancelReachesUpstream(t *testing.T) {
	f := newFixture(t)
	canceled := make(chan struct{})
	f.upstream(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {}\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
			close(canceled)
		case <-time.After(10 * time.Second):
		}
	})
	ctx, cancel := context.WithCancel(t.Context())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.srv.URL+"/v1/chat/completions", bytes.NewReader(chatBody("fast")))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+keyFull)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if _, err := bufio.NewReader(resp.Body).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case <-canceled:
	case <-time.After(5 * time.Second):
		t.Fatal("upstream did not observe the client's cancellation")
	}
}

func TestProxy_OnResponseHook(t *testing.T) {
	f := newFixture(t)
	f.upstream(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("x-bifrost-routing-info-provider", "up-a")
		w.Header().Set("x-bifrost-routing-info-model", "m1")
		okJSON(w, r)
	})
	body := []byte("{\"model\":\"fast\",\"messages\":[{\"content\":\"\u200c\"}]}")
	var (
		gotBody     []byte
		gotServedBy string
		gotPath     string
	)
	f.px.OnResponse(func(r *http.Request, reqBody []byte, resp *http.Response) {
		gotBody = append([]byte(nil), reqBody...)
		gotServedBy = resp.Header.Get("x-remora-served-by")
		gotPath = r.URL.Path
		resp.Header.Set("x-remora-dropped-params", "temperature")
		resp.Header.Set("x-remora-served-by", "codex/m1")
	})
	resp := f.do(t, http.MethodPost, "/v1/chat/completions", keyFull, "application/json", body, nil)
	if !bytes.Equal(gotBody, body) || gotServedBy != "up-a/m1" || gotPath != "/v1/chat/completions" {
		t.Fatalf("hook saw body %q, served-by %q, path %q", gotBody, gotServedBy, gotPath)
	}
	if resp.Header.Get("x-remora-dropped-params") != "temperature" || resp.Header.Get("x-remora-served-by") != "codex/m1" {
		t.Fatalf("hook headers did not reach the client: %v", resp.Header)
	}
}

func TestProxy_UpstreamDown(t *testing.T) {
	f := newFixture(t)
	f.px.transport.DialContext = func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("dial bifrost 127.0.0.1:18080: refused")
	}
	for _, c := range []struct{ path, want string }{
		{"/v1/chat/completions", `"code":"upstream_unavailable"`},
		{"/v1/messages", `"type":"error"`},
		{"/v1/systemone", `"detail":`},
	} {
		resp := f.do(t, http.MethodPost, c.path, keyFull, "application/json", chatBody("up-a/m1"), nil)
		body := string(resp.Body)
		if resp.StatusCode != http.StatusBadGateway || !strings.Contains(body, c.want) ||
			strings.Contains(strings.ToLower(body), "bifrost") || strings.Contains(body, "18080") {
			t.Fatalf("%s: got %d %s", c.path, resp.StatusCode, body)
		}
	}
}

func TestProxy_NoResponseTimeout(t *testing.T) {
	f := newFixture(t)
	if f.px.transport.ResponseHeaderTimeout != 0 {
		t.Fatalf("ResponseHeaderTimeout = %v, want 0", f.px.transport.ResponseHeaderTimeout)
	}
	if f.srv.Config.WriteTimeout != 0 {
		t.Fatalf("server WriteTimeout = %v, want 0", f.srv.Config.WriteTimeout)
	}
	f.upstream(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(3 * time.Second)
		okJSON(w, r)
	})
	body, ct := multipartBody(t, []field{{name: "file", filename: "a.wav", value: []byte("RIFF")}, {name: "model", value: []byte("up-a/m1")}})
	resp := f.do(t, http.MethodPost, "/v1/audio/transcriptions", keyFull, ct, body, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestProxy_NotReadyUntilSynced(t *testing.T) {
	f := newFixture(t)
	var ready atomic.Bool
	f.px.Ready = ready.Load
	resp := f.do(t, http.MethodPost, "/v1/chat/completions", keyFull, "application/json", chatBody("fast"), nil)
	if resp.StatusCode != http.StatusServiceUnavailable || !strings.Contains(string(resp.Body), `"not_ready"`) {
		t.Fatalf("before sync: %d %s, want 503 not_ready", resp.StatusCode, resp.Body)
	}
	if resp.Header.Get("Retry-After") == "" {
		t.Error("before sync: no Retry-After")
	}
	resp = f.do(t, http.MethodPost, "/v1/messages", keyFull, "application/json", chatBody("fast"), nil)
	if resp.StatusCode != http.StatusServiceUnavailable || !strings.Contains(string(resp.Body), `"type":"error"`) {
		t.Errorf("before sync on /v1/messages: %d %s, want 503 in Anthropic's shape", resp.StatusCode, resp.Body)
	}
	if n := f.hits.Load(); n != 0 {
		t.Errorf("front door called %d times before sync", n)
	}
	ready.Store(true)
	if resp := f.do(t, http.MethodPost, "/v1/chat/completions", keyFull, "application/json", chatBody("fast"), nil); resp.StatusCode != http.StatusOK {
		t.Errorf("after sync: %d %s", resp.StatusCode, resp.Body)
	}
}

func TestProxy_AliasKindMustMatchEndpoint(t *testing.T) {
	f := newFixture(t)
	for _, c := range []struct {
		path, model string
		want        int
	}{
		{"/v1/chat/completions", "emb", http.StatusBadRequest},
		{"/v1/embeddings", "fast", http.StatusBadRequest},
		{"/v1/decisions", "memory/fast", http.StatusBadRequest},
		{"/v1/chat/completions", "fast", http.StatusOK},
		{"/v1/embeddings", "emb", http.StatusOK},
		{"/v1/embeddings", "up-a/m1", http.StatusOK}, // a provider model is not an alias: the front door decides
	} {
		before := f.hits.Load()
		resp := f.do(t, http.MethodPost, c.path, keyFull, "application/json", chatBody(c.model), nil)
		if resp.StatusCode != c.want {
			t.Errorf("%s %s: %d %s, want %d", c.path, c.model, resp.StatusCode, resp.Body, c.want)
		}
		if c.want == http.StatusBadRequest {
			if !strings.Contains(string(resp.Body), `"model_kind_mismatch"`) {
				t.Errorf("%s %s: body %s, want model_kind_mismatch", c.path, c.model, resp.Body)
			}
			if f.hits.Load() != before {
				t.Errorf("%s %s: refused request reached the front door", c.path, c.model)
			}
		}
	}
}

func TestProxy_AliasesUnreadableFailsClosed(t *testing.T) {
	f := newFixture(t)
	db, err := sql.Open("sqlite", f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(t.Context(), `UPDATE aliases SET capabilities = 'not a list' WHERE name = 'emb'`); err != nil {
		t.Fatal(err)
	}
	resp := f.do(t, http.MethodPost, "/v1/chat/completions", keyFull, "application/json", chatBody("emb"), nil)
	if resp.StatusCode != http.StatusInternalServerError || !strings.Contains(string(resp.Body), `"internal_error"`) {
		t.Fatalf("unreadable aliases: %d %s, want 500 internal_error", resp.StatusCode, resp.Body)
	}
	if n := f.hits.Load(); n != 0 {
		t.Errorf("front door called %d times with unreadable aliases", n)
	}
}

func TestProxy_SSEOversizedErrorReplaced(t *testing.T) {
	f := newFixture(t)
	big := "data: {\"error\":{\"message\":\"bifrost sk-bf-leak " + strings.Repeat("x", maxSSELine+(64<<10)) + "\"}}\n\n"
	f.upstream(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[]}\n\n"+big+"data: [DONE]\n\n")
	})
	resp := f.do(t, http.MethodPost, "/v1/chat/completions", keyFull, "application/json", chatBody("fast"), nil)
	body := string(resp.Body)
	if strings.Contains(body, "bifrost") || strings.Contains(body, "sk-bf-") || strings.Contains(body, "xxxx") {
		t.Fatalf("oversized error line forwarded: %d bytes", len(body))
	}
	if !strings.Contains(body, `"upstream_error"`) || !strings.HasSuffix(body, "data: [DONE]\n\n") {
		t.Errorf("body = %q, want remora's error then the rest of the stream", body)
	}
}
