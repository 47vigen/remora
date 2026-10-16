package proxy

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"
)

func TestSSEScrubberBatchesWithoutBlocking(t *testing.T) {
	pr, pw := io.Pipe()
	s := newSSEScrubber(pr, shapeOpenAI)
	go func() {
		_, _ = io.WriteString(pw, "data: {\"a\":1}\n\ndata: {\"error\":{\"message\":\"bifrost x\"}}\n\ndata: {\"b\":2}\n\ndata: {\"c\"")
	}()
	got := make(chan string, 1)
	go func() {
		buf := make([]byte, 32<<10)
		n, _ := s.Read(buf)
		got <- string(buf[:n])
	}()
	select {
	case first := <-got:
		if !strings.Contains(first, `{"a":1}`) || !strings.Contains(first, `{"b":2}`) {
			t.Fatalf("one Read returned %q, want every complete line", first)
		}
		if strings.Contains(first, "bifrost") || strings.Contains(first, `{"c"`) {
			t.Fatalf("one Read returned %q: an unscrubbed error or a partial line", first)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Read blocked with complete lines buffered")
	}
	go func() { _, _ = io.WriteString(pw, ":3}\n\n"); _ = pw.Close() }()
	rest, _ := io.ReadAll(s)
	if !strings.HasPrefix(string(rest), "data: {\"c\":3}\n") {
		t.Fatalf("the partial line arrived as %q", rest)
	}
}

func TestSSEScrubberOneReadPerEvent(t *testing.T) {
	s := newSSEScrubber(io.NopCloser(bytes.NewReader(sseStream(100))), shapeOpenAI)
	buf, reads := make([]byte, 32<<10), 0
	for {
		n, err := s.Read(buf)
		if n > 0 {
			reads++
		}
		if err != nil {
			break
		}
	}
	if reads > 101 {
		t.Fatalf("reads = %d for 100 events, want at most 101", reads)
	}
}

func TestSSEScrubberOutputUnchanged(t *testing.T) {
	long := "data: {\"x\":\"" + strings.Repeat("y", 20<<10) + "\"}\n\n"
	cases := map[string]struct{ in, want string }{
		"plain":            {string(sseStream(3)), string(sseStream(3))},
		"persian":          {"data: {\"c\":\"\u0645\u06cc\u200c\u0634\u0648\u062f\"}\n\ndata: [DONE]\n\n", "data: {\"c\":\"\u0645\u06cc\u200c\u0634\u0648\u062f\"}\n\ndata: [DONE]\n\n"},
		"long":             {long, long},
		"error":            {"data: {\"error\":{\"message\":\"bifrost sk-bf-k\"}}\n\n", "data: {\"error\":{\"message\":\"remora [redacted]\"}}\n\n"},
		"no final newline": {"data: {\"a\":1}", "data: {\"a\":1}"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			for _, chunk := range []int{1, 7, 1 << 20} {
				got, err := io.ReadAll(newSSEScrubber(io.NopCloser(&chunked{b: []byte(c.in), n: chunk}), shapeOpenAI))
				if err != nil || string(got) != c.want {
					t.Fatalf("chunks of %d: %q, %v; want %q", chunk, got, err, c.want)
				}
			}
		})
	}
}

// chunked hands its bytes out at most n at a time, the way a slow upstream does.
type chunked struct {
	b []byte
	n int
}

func (c *chunked) Read(p []byte) (int, error) {
	if len(c.b) == 0 {
		return 0, io.EOF
	}
	n := copy(p[:min(len(p), c.n)], c.b)
	c.b = c.b[n:]
	return n, nil
}
