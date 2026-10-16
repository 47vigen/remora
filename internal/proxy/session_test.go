package proxy

import (
	"bytes"
	"encoding/json"
	"net/http"
	"regexp"
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
