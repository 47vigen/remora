package proxy

import (
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
)

func TestCodexDroppedParams(t *testing.T) {
	cases := []struct {
		name, path, body string
		want             []string
	}{
		{
			name: "chat drops sampling, limits and stop",
			path: "/v1/chat/completions",
			body: `{"model":"codex/gpt-x","messages":[],"temperature":0.5,"top_p":0.9,"max_tokens":10,` +
				`"parallel_tool_calls":false,"stop":["x"]}`,
			want: []string{"max_tokens", "parallel_tool_calls", "stop", "temperature", "top_p"},
		},
		{
			name: "chat keeps what the translator carries",
			path: "/v1/chat/completions",
			body: `{"model":"codex/gpt-x","messages":[],"tools":[],"parallel_tool_calls":true,` +
				`"stream_options":{"include_usage":true}}`,
		},
		{
			name: "chat keeps a known service tier",
			path: "/v1/chat/completions",
			body: `{"model":"m","messages":[],"service_tier":"Fast","reasoning_effort":"low","response_format":{}}`,
		},
		{
			name: "chat drops an unknown service tier",
			path: "/v1/chat/completions",
			body: `{"model":"m","messages":[],"service_tier":"flex","user":"u","n":1}`,
			want: []string{"n", "service_tier", "user"},
		},
		{
			name: "responses drops limits, user, store and tier",
			path: "/v1/responses",
			body: `{"model":"m","input":"hi","max_output_tokens":10,"user":"u","store":true,"service_tier":"flex"}`,
			want: []string{"max_output_tokens", "service_tier", "store", "user"},
		},
		{
			name: "responses keeps priority tier and store false",
			path: "/v1/responses",
			body: `{"model":"m","input":"hi","service_tier":"priority","store":false}`,
		},
		{
			name: "responses drops the rest of its list",
			path: "/v1/responses",
			body: `{"model":"m","input":"hi","temperature":1,"top_p":1,"max_completion_tokens":5,"truncation":"auto",` +
				`"prompt_cache_options":{},"prompt_cache_retention":"24h","context_management":[],` +
				`"parallel_tool_calls":false,"service_tier":7,"metadata":{}}`,
			want: []string{
				"context_management", "max_completion_tokens", "parallel_tool_calls", "prompt_cache_options",
				"prompt_cache_retention", "service_tier", "temperature", "top_p", "truncation",
			},
		},
		{
			name: "messages",
			path: "/v1/messages",
			body: `{"model":"m","messages":[],"max_tokens":10,"temperature":0.2}`,
			want: []string{"max_tokens", "temperature"},
		},
		{
			name: "messages keeps the rest",
			path: "/v1/messages",
			body: `{"model":"m","messages":[],"system":"s","stop_sequences":["x"]}`,
		},
		{
			name: "duplicate keys are reported once",
			path: "/v1/messages",
			body: `{"model":"m","messages":[],"top_p":1,"top_p":2}`,
			want: []string{"top_p"},
		},
		{name: "embeddings", path: "/v1/embeddings", body: `{"model":"m","input":"x","temperature":1}`},
		{name: "array body", path: "/v1/chat/completions", body: `[]`},
		{name: "not json", path: "/v1/chat/completions", body: `not json`},
		{name: "empty body", path: "/v1/responses", body: ``},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := CodexDroppedParams(c.path, []byte(c.body))
			if !slices.Equal(got, c.want) {
				t.Fatalf("CodexDroppedParams(%s, %s) = %q, want %q", c.path, c.body, got, c.want)
			}
		})
	}
}

// servedBy makes the fake front door report provider/model as the member that served the request, answering
// JSON, or SSE when stream is true.
func servedBy(provider, model string, stream bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("x-bifrost-routing-info-provider", provider)
		w.Header().Set("x-bifrost-routing-info-model", model)
		if stream {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"id\":\"x\"}\n\ndata: [DONE]\n\n")
			return
		}
		okJSON(w, r)
	}
}

func TestDroppedParamsHeader(t *testing.T) {
	const header = "x-remora-dropped-params"
	cases := []struct {
		name, provider, model, path, body string
		stream                            bool
		want                              string
	}{
		{
			name: "codex with temperature", provider: "codex", model: "gpt-x", path: "/v1/chat/completions",
			body: `{"model":"codex/gpt-x","messages":[],"temperature":0.5}`, want: "temperature",
		},
		{
			name: "codex stream, several", provider: "codex", model: "gpt-x", path: "/v1/chat/completions",
			body: `{"model":"codex/gpt-x","messages":[],"stream":true,"top_p":1,"temperature":0.5}`, stream: true,
			want: "temperature,top_p",
		},
		{
			name: "codex responses", provider: "codex", model: "gpt-x", path: "/v1/responses",
			body: `{"model":"codex/gpt-x","input":"hi","max_output_tokens":5,"user":"u"}`, want: "max_output_tokens,user",
		},
		{
			name: "codex messages", provider: "codex", model: "gpt-x", path: "/v1/messages",
			body: `{"model":"codex/gpt-x","messages":[],"max_tokens":5}`, want: "max_tokens",
		},
		{
			name: "other provider", provider: "openrouter", model: "m", path: "/v1/chat/completions",
			body: `{"model":"openrouter/m","messages":[],"temperature":0.5}`,
		},
		{
			name: "codex, nothing dropped", provider: "codex", model: "gpt-x", path: "/v1/chat/completions",
			body: `{"model":"codex/gpt-x","messages":[]}`,
		},
		{
			name: "codex-like provider name", provider: "codexish", model: "gpt-x", path: "/v1/chat/completions",
			body: `{"model":"codexish/gpt-x","messages":[],"temperature":0.5}`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			f.upstream(servedBy(c.provider, c.model, c.stream))
			resp := f.do(t, http.MethodPost, c.path, keyFull, "application/json", []byte(c.body), nil)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status %d: %s", resp.StatusCode, resp.Body)
			}
			if got, ok := resp.Header[http.CanonicalHeaderKey(header)]; c.want == "" && ok {
				t.Fatalf("%s = %q, want absent", header, got)
			}
			if got := resp.Header.Get(header); got != c.want {
				t.Fatalf("%s = %q, want %q", header, got, c.want)
			}
			if c.stream && !strings.Contains(string(resp.Body), "[DONE]") {
				t.Fatalf("stream body = %q", resp.Body)
			}
		})
	}
}

// TestDroppedParamsHeaderAfterServedByHooks proves the header follows the served-by a response hook leaves, so a
// hook that renames an internal provider (plan 03, Task 6) decides whether a Codex member served the request.
func TestDroppedParamsHeaderAfterServedByHooks(t *testing.T) {
	f := newFixture(t)
	f.upstream(servedBy("internal", "codex/gpt-x", false))
	f.px.OnResponse(func(_ *http.Request, _ []byte, resp *http.Response) {
		if sb := resp.Header.Get(headerServedBy); strings.HasPrefix(sb, "internal/") {
			resp.Header.Set(headerServedBy, strings.TrimPrefix(sb, "internal/"))
		}
	})
	resp := f.do(t, http.MethodPost, "/v1/chat/completions", keyFull, "application/json",
		[]byte(`{"model":"codex/gpt-x","messages":[],"temperature":0.5}`), nil)
	if got := resp.Header.Get("x-remora-dropped-params"); got != "temperature" {
		t.Fatalf("x-remora-dropped-params = %q, want temperature (served-by %q)", got,
			resp.Header.Get("x-remora-served-by"))
	}
}
