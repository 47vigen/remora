package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func benchChatBody(msgs int) []byte {
	type m struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	var ms []m
	for range msgs {
		ms = append(ms, m{Role: "user", Content: strings.Repeat("lorem ipsum dolor sit amet \"quoted\" \\n ", 50)})
	}
	b, _ := json.Marshal(map[string]any{
		"model": "alias-3", "stream": true, "messages": ms,
		"tools": []map[string]any{{"type": "function", "function": map[string]any{"name": "x", "parameters": map[string]any{"type": "object"}}}},
	})
	return b
}

var sizes = []int{2, 100, 500}

// BenchmarkRequestPath is the whole per-request body cost: read and extract the model, swap in the engine model and
// derive the session.
func BenchmarkRequestPath(b *testing.B) {
	h := http.Header{}
	for _, n := range sizes {
		body := benchChatBody(n)
		b.Run(fmt.Sprintf("%dKB", len(body)/1024), func(b *testing.B) {
			b.SetBytes(int64(len(body)))
			b.ReportAllocs()
			for b.Loop() {
				r, _ := http.NewRequestWithContext(b.Context(), http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
				r.Header.Set("Content-Type", "application/json")
				_, raw, sp, err := extractModel(r)
				if err != nil {
					b.Fatal(err)
				}
				out, err := withModel(raw, sp, "application/json", "remora-alias/alias-3")
				if err != nil {
					b.Fatal(err)
				}
				_, _ = out, opencodeSession(h, raw, sp)
			}
		})
	}
}

func BenchmarkExtractModel(b *testing.B) {
	for _, n := range sizes {
		body := benchChatBody(n)
		b.Run(fmt.Sprintf("%dKB", len(body)/1024), func(b *testing.B) {
			b.SetBytes(int64(len(body)))
			b.ReportAllocs()
			for b.Loop() {
				r, _ := http.NewRequestWithContext(b.Context(), http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
				r.Header.Set("Content-Type", "application/json")
				if _, _, _, err := extractModel(r); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkOpencodeSession(b *testing.B) {
	h := http.Header{}
	for _, n := range sizes {
		body := benchChatBody(n)
		_, sp, err := scanBody(body)
		if err != nil {
			b.Fatal(err)
		}
		b.Run(fmt.Sprintf("%dKB", len(body)/1024), func(b *testing.B) {
			b.SetBytes(int64(len(body)))
			b.ReportAllocs()
			for b.Loop() {
				_ = opencodeSession(h, body, sp)
			}
		})
	}
}

func BenchmarkWithModel(b *testing.B) {
	for _, n := range sizes {
		body := benchChatBody(n)
		_, sp, err := scanBody(body)
		if err != nil {
			b.Fatal(err)
		}
		b.Run(fmt.Sprintf("%dKB", len(body)/1024), func(b *testing.B) {
			b.SetBytes(int64(len(body)))
			b.ReportAllocs()
			for b.Loop() {
				if _, err := withModel(body, sp, "application/json", "remora-alias/alias-3"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// sseStream: n content events, Bifrost style "data: {...}\n\n".
func sseStream(n int) []byte {
	var buf bytes.Buffer
	for i := range n {
		fmt.Fprintf(&buf, `data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","created":1700000000,"model":"gemini-3-pro","choices":[{"index":0,"delta":{"content":"token %d with some text"},"finish_reason":null}]}`+"\n\n", i)
	}
	buf.WriteString("data: [DONE]\n\n")
	return buf.Bytes()
}

func BenchmarkSSEScrubber(b *testing.B) {
	stream := sseStream(1000)
	b.SetBytes(int64(len(stream)))
	b.ReportAllocs()
	buf := make([]byte, 32<<10) // ReverseProxy's buffer size
	var reads int
	for b.Loop() {
		s := newSSEScrubber(io.NopCloser(bytes.NewReader(stream)), shapeOpenAI)
		for {
			n, err := s.Read(buf)
			if n > 0 {
				reads++
			}
			if err != nil {
				break
			}
		}
	}
	b.ReportMetric(float64(reads)/float64(b.N)/1000, "reads/event")
}

func BenchmarkCleanResponseHeaders(b *testing.B) {
	b.ReportAllocs()
	info := &reqInfo{model: "alias-3", isAlias: true}
	for b.Loop() {
		h := http.Header{
			"Content-Type": {"text/event-stream"}, "Cache-Control": {"no-cache"}, "Connection": {"keep-alive"},
			"Date": {"Mon, 01 Jan 2026 00:00:00 GMT"}, "X-Request-Id": {"abc-123"}, "Vary": {"Origin"},
			"X-Bifrost-Routing-Info-Provider": {"remora-oauth"}, "X-Bifrost-Routing-Info-Model": {"antigravity/gemini-3-pro"},
			"X-Bifrost-Routing-Info-Is-Fallback": {"false"}, "Server": {"fasthttp"},
		}
		setRemoraHeaders(h, info)
	}
}
