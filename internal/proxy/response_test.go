package proxy

import (
	"bytes"
	"io"
	"net/http"
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

func TestMayHoldEngineWord(t *testing.T) {
	for in, want := range map[string]bool{
		"BiFrOsT": true, "x-CLIPROXYAPI": true, "sk-bf-1": true, "routing via bifrost/x": true,
		"Mon, 01 Jan 2026": false, "": false, "bifros": false, "cliproxy": false,
	} {
		if got := mayHoldEngineWord([]byte(in)); got != want {
			t.Errorf("mayHoldEngineWord(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestScrubUnchangedWhenClean(t *testing.T) {
	if n := testing.AllocsPerRun(100, func() { _ = scrubString("Mon, 01 Jan 2026 00:00:00 GMT") }); n != 0 {
		t.Fatalf("allocs = %v, want 0 for a clean value", n)
	}
}

func FuzzScrubFastPath(f *testing.F) {
	for _, s := range []string{"bifrost", "BIFROST x", "sk-bf-abc", "cliproxyapi", "clean", "bIfRoSt sk-bf-x_y-1"} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		want := engineWords.ReplaceAll(engineKeys.ReplaceAll(b, []byte("[redacted]")), []byte("remora"))
		if got := scrub(b); !bytes.Equal(got, want) {
			t.Fatalf("scrub(%q) = %q, want %q", b, got, want)
		}
	})
}

func TestStripExtraFieldsTopLevelOnly(t *testing.T) {
	persian := "می\u200cشود"
	cases := []struct{ name, in, want string }{
		{"first", `{"extra_fields":{"k":1},"id":"x"}`, `{"id":"x"}`},
		{"middle", `{"id":"x","extra_fields":{"routing_info":{"key":"k"}},"object":"o"}`, `{"id":"x","object":"o"}`},
		{"last", `{"id":"x", "extra_fields" : {"a":[1,"}"]} }`, `{"id":"x" }`},
		{"only", `{"extra_fields":{}}`, `{}`},
		{
			"nested kept", `{"choices":[{"message":{"content":"\"extra_fields\":1","extra_fields":2}}],"extra_fields":{"p":"q"}}`,
			`{"choices":[{"message":{"content":"\"extra_fields\":1","extra_fields":2}}]}`,
		},
		{"persian", `{"c":"` + persian + `","extra_fields":{}}`, `{"c":"` + persian + `"}`},
		{"absent", `{"id":"x"}`, `{"id":"x"}`},
		{"not an object", `["extra_fields"]`, `["extra_fields"]`},
	}
	for _, c := range cases {
		if got := stripExtraFields([]byte(c.in)); string(got) != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
	clean := []byte(`{"id":"x","choices":[]}`)
	if n := testing.AllocsPerRun(100, func() { _ = stripExtraFields(clean) }); n != 0 {
		t.Errorf("allocs on a body without extra_fields = %v, want 0", n)
	}
}

func TestCleanResponseHeadersDropsCpa(t *testing.T) {
	h := http.Header{"X-Cpa-Trace-Id": {"t1"}, "X-Cpa-Anything": {"v"}, "X-Request-Id": {"r1"}}
	cleanResponseHeaders(h)
	if h.Get("X-Cpa-Trace-Id") != "" || h.Get("X-Cpa-Anything") != "" || h.Get("X-Request-Id") != "r1" {
		t.Fatalf("headers = %v", h)
	}
}

func TestSSEStripsExtraFields(t *testing.T) {
	in := "data: {\"id\":\"c\",\"choices\":[{\"delta\":{\"content\":\"hi\"}}],\"extra_fields\":{\"provider\":\"remora-oauth\"}}\n\n" +
		"data: {\"error\":{\"message\":\"bifrost down\"},\"extra_fields\":{\"key\":\"k\"}}\n\ndata: [DONE]\n\n"
	got, _ := io.ReadAll(newSSEScrubber(io.NopCloser(strings.NewReader(in)), shapeOpenAI))
	want := "data: {\"id\":\"c\",\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n" +
		"data: {\"error\":{\"message\":\"remora down\"}}\n\ndata: [DONE]\n\n"
	if string(got) != want {
		t.Fatalf("stream = %q, want %q", got, want)
	}
}
