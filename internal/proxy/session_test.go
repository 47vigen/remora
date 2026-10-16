package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// sessionOf is opencodeSession over a body scanned the way the proxy scans it.
func sessionOf(h http.Header, body []byte) string {
	_, sp, _ := scanBody(body)
	return opencodeSession(h, body, sp)
}

// firstMessageRef is the struct-decoding firstMessage the spans replaced, kept as the parity oracle.
func firstMessageRef(body []byte) []byte {
	var b struct {
		Messages []json.RawMessage `json:"messages"`
		Input    json.RawMessage   `json:"input"`
	}
	if json.Unmarshal(body, &b) != nil {
		return nil
	}
	if len(b.Messages) > 0 {
		return b.Messages[0]
	}
	var input []json.RawMessage
	if json.Unmarshal(b.Input, &input) == nil && len(input) > 0 {
		return input[0]
	}
	if len(b.Input) > 0 && b.Input[0] == '"' {
		return b.Input
	}
	return nil
}

func TestOpencodeSession(t *testing.T) {
	format := regexp.MustCompile(`^ses_[0-9a-f]{12}[0-9A-Za-z]{14}$`)
	none := http.Header{}

	turn1 := sessionOf(none, []byte(`{"model":"m","messages":[{"role":"user","content":"سلام"}]}`))
	turn2 := sessionOf(none, []byte(`{"model":"m","messages":[{"role":"user","content":"سلام"},{"role":"assistant","content":"hi"},{"role":"user","content":"more"}]}`))
	other := sessionOf(none, []byte(`{"model":"m","messages":[{"role":"user","content":"another"}]}`))
	if !format.MatchString(turn1) || turn1 != turn2 || turn1 == other {
		t.Fatalf("turns of one conversation must share an id, another must not: %q %q %q", turn1, turn2, other)
	}

	input := sessionOf(none, []byte(`{"model":"m","input":[{"role":"user","content":"x"}]}`))
	str := sessionOf(none, []byte(`{"model":"m","input":"x"}`))
	if !format.MatchString(input) || !format.MatchString(str) || input == str {
		t.Fatalf("responses input: %q %q", input, str)
	}

	r1, r2 := sessionOf(none, []byte(`{"model":"m"}`)), sessionOf(none, nil)
	if !format.MatchString(r1) || r1 == r2 {
		t.Fatalf("no message must get a fresh random id: %q %q", r1, r2)
	}

	if got := sessionOf(http.Header{headerOpencodeSession: {" ses_mine "}}, nil); got != "ses_mine" {
		t.Fatalf("the client's own id must be kept: %q", got)
	}
	if got := sessionOf(http.Header{headerOpencodeSession: {"bad\x01id"}}, nil); !format.MatchString(got) {
		t.Fatalf("a control character must not pass: %q", got)
	}
}

func TestRewriteSetsOpencodeSession(t *testing.T) {
	h := http.Header{"X-Bf-Eh-X-Opencode-Session": {"forged"}}
	setUpstreamHeaders(h, "vk")
	if h.Get(headerForwardSession) != "" {
		t.Fatal("a client must not set the forwarded session header itself")
	}
}

func FuzzFirstMessageParity(f *testing.F) {
	for _, s := range []string{
		`{"model":"m","messages":[{"role":"user","content":"x"}]}`, `{"model":"m","input":[{"a":1}]}`,
		`{"model":"m","input":"x"}`, `{"model":"m","Messages":[1],"messages":[2]}`, `{"model":"m","messages":{},"input":[1]}`,
		`{"model":"m","messages":null,"input":[1]}`, `{"model":"m","messages":[],"input":"s"}`, `{"model":"m","MESSAGES":"x"}`,
		`{"model":"m","messages":{},"messages":[1]}`, `{"model":"m","input":[]}`, `{"model":"m","input":null}`,
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		if !json.Valid(raw) {
			return
		}
		_, sp, err := scanBody(raw)
		if err != nil {
			return // the proxy refuses these before it derives a session
		}
		if got, want := firstMessage(raw, sp), firstMessageRef(raw); !bytes.Equal(got, want) {
			t.Fatalf("firstMessage(%q) = %q, want %q", raw, got, want)
		}
	})
}

// TestOpencodeSessionGolden pins the derived ids to the values the decoder-based code produced, so turns of a
// conversation already in flight keep their id across the upgrade.
func TestOpencodeSessionGolden(t *testing.T) {
	cases := []struct{ body, want string }{
		{`{"model":"m","messages":[{"role":"system","content":"You are helpful."},{"role":"user","content":"hi"}]}`, "ses_f357595d7cfazPJfHBWIRwCbPa"},
		{"{\"model\":\"m\",\"messages\":[{\"role\":\"user\",\"content\":\"\u0645\u06cc\u200c\u0634\u0648\u062f\"}]}", "ses_759f614c0705r07IQkxA5ru3kn"},
		{`{"model":"m","input":[{"role":"user","content":"x"}]}`, "ses_6f34aad89c85bxf0oe7uoMbV7F"},
		{`{"model":"m","input":"just a string"}`, "ses_eb449bf2a67ch4gDEC2bcNejWA"},
		{`{"model":"m","Messages":[{"role":"user","content":"caps"}]}`, "ses_f335fd0973fbQrX78Ul5Uj46wg"},
		{`{"model":"m","messages":[{"a":1}],"messages":[{"b":2}]}`, "ses_135367ffabd2H6sbrvpDDQLpG1"},
		{`{"model":"m","messages":null,"input":[{"c":3}]}`, "ses_7d6f73b8cd50mkhlAawDAPbVyV"},
	}
	for _, c := range cases {
		if got := sessionOf(http.Header{}, []byte(c.body)); got != c.want {
			t.Errorf("session(%s) = %q, want %q", c.body, got, c.want)
		}
	}
	a, b := sessionOf(http.Header{}, []byte(`{"model":"m","messages":{},"input":[1]}`)), sessionOf(http.Header{}, []byte(`{"model":"m","messages":{},"input":[1]}`))
	if a == b {
		t.Errorf("a messages value that is not a list must give a random id, got %q twice", a)
	}
}

// affinityOf is the affinity key of a body scanned the way the proxy scans it.
func affinityOf(h http.Header, vk string, body []byte) string {
	_, sp, _ := scanBody(body)
	return affinityKey(h, vk, sp.promptCacheKeyIn(body), affinitySource(body, sp))
}

func TestAffinityKeyPrecedence(t *testing.T) {
	format := regexp.MustCompile(`^aff_[0-9a-f]{32}$`)
	body := []byte(`{"model":"m","prompt_cache_key":"p1","messages":[{"role":"user","content":"x"}]}`)
	cases := []struct {
		name string
		h    http.Header
		want string
	}{
		{"session id first", hdr(headerSessionID, "s1", headerOpencodeSession, "o1"), "s1"},
		{"then opencode", hdr(headerOpencodeSession, "o1"), "o1"},
		{"then prompt_cache_key", http.Header{}, "p1"},
	}
	for _, c := range cases {
		if got := affinityOf(c.h, "vk", body); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	if got := affinityOf(http.Header{}, "vk", []byte(`{"model":"m","messages":[{"role":"user","content":"x"}]}`)); !format.MatchString(got) {
		t.Errorf("derived key %q", got)
	}
	if got := affinityOf(http.Header{}, "vk", []byte(`{"model":"m","input":[1,2]}`)); got != "" {
		t.Errorf("no user message must give no key: %q", got)
	}
	if got := affinityOf(http.Header{}, "vk", []byte(`{"model":"m","prompt_cache_key":7,"messages":[]}`)); got != "" {
		t.Errorf("a non-string prompt_cache_key must be ignored: %q", got)
	}
}

func TestAffinityKeyIgnoresSystemPrompt(t *testing.T) {
	none := http.Header{}
	sys := `{"role":"system","content":"a long shared system prompt"}`
	a := affinityOf(none, "vk", []byte(`{"model":"m","messages":[`+sys+`,{"role":"user","content":"سلام"}]}`))
	b := affinityOf(none, "vk", []byte(`{"model":"m","messages":[`+sys+`,{"role":"user","content":"سلام2"}]}`))
	a3 := affinityOf(none, "vk", []byte(`{"model":"m","messages":[`+sys+`,{"role":"user","content":"سلام"},{"role":"assistant","content":"hi"},{"role":"user","content":"next"}]}`))
	otherSys := affinityOf(none, "vk", []byte(`{"model":"m","messages":[{"role":"system","content":"other"},{"role":"user","content":"سلام"}]}`))
	if a == b || a != a3 || a != otherSys {
		t.Fatalf("keys must follow the first user message: a=%q b=%q a3=%q otherSys=%q", a, b, a3, otherSys)
	}
	r1 := affinityOf(none, "vk", []byte(`{"model":"m","instructions":"be brief","input":"hello"}`))
	r2 := affinityOf(none, "vk", []byte(`{"model":"m","input":"hello"}`))
	if r1 == "" || r1 != r2 {
		t.Fatalf("responses string input: %q %q", r1, r2)
	}
	if affinityOf(none, "a", []byte(`{"model":"m","input":"hello"}`)) == affinityOf(none, "b", []byte(`{"model":"m","input":"hello"}`)) {
		t.Fatal("two client keys must not share a derived key")
	}
}

func TestAffinityKeyRejectsUnusableClientID(t *testing.T) {
	body := []byte(`{"model":"m","prompt_cache_key":"p1"}`)
	for _, bad := range []string{strings.Repeat("a", maxSessionLen+1), "a\nb"} {
		if got := affinityOf(hdr(headerSessionID, bad), "vk", body); got != "p1" {
			t.Errorf("unusable id %q: got %q, want the next source", bad[:3], got)
		}
	}
}

func TestRewriteSetsAffinity(t *testing.T) {
	rewritten := func(h http.Header, body string) http.Header {
		in := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/chat/completions", nil)
		in.Header = h
		info := &reqInfo{in: in, upstream: "/v1/chat/completions", vk: "vk", body: []byte(body)}
		_, info.spans, _ = scanBody(info.body)
		in = in.WithContext(context.WithValue(in.Context(), reqInfoKey{}, info))
		out := in.Clone(in.Context())
		out.Header = h.Clone()
		p := &Proxy{upstream: &url.URL{Scheme: "http", Host: "front"}}
		p.rewrite(&httputil.ProxyRequest{In: in, Out: out})
		return out.Header
	}
	chat := `{"model":"m","messages":[{"role":"user","content":"x"}]}`
	if got := rewritten(hdr(headerSessionID, "abc"), chat).Get(headerForwardAffinity); got != "abc" {
		t.Errorf("affinity = %q, want abc", got)
	}
	if got := rewritten(hdr(headerForwardAffinity, "evil"), `{"model":"m","input":[1]}`).Get(headerForwardAffinity); got != "" {
		t.Errorf("a forged header must not pass: %q", got)
	}
	h := rewritten(http.Header{}, `{"model":"emb","input":[1,2]}`)
	if h.Get(headerForwardAffinity) != "" || h.Get(headerForwardSession) == "" {
		t.Errorf("embeddings: affinity %q, session %q", h.Get(headerForwardAffinity), h.Get(headerForwardSession))
	}
}

// hdr builds a canonical header from name, value pairs, as a server would receive it.
func hdr(kv ...string) http.Header {
	h := http.Header{}
	for i := 0; i+1 < len(kv); i += 2 {
		h.Set(kv[i], kv[i+1])
	}
	return h
}
