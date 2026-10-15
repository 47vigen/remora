package proxy

import (
	"net/http"
	"regexp"
	"testing"
)

func TestOpencodeSession(t *testing.T) {
	format := regexp.MustCompile(`^ses_[0-9a-f]{12}[0-9A-Za-z]{14}$`)
	none := http.Header{}

	turn1 := opencodeSession(none, []byte(`{"model":"m","messages":[{"role":"user","content":"سلام"}]}`))
	turn2 := opencodeSession(none, []byte(`{"model":"m","messages":[{"role":"user","content":"سلام"},{"role":"assistant","content":"hi"},{"role":"user","content":"more"}]}`))
	other := opencodeSession(none, []byte(`{"model":"m","messages":[{"role":"user","content":"another"}]}`))
	if !format.MatchString(turn1) || turn1 != turn2 || turn1 == other {
		t.Fatalf("turns of one conversation must share an id, another must not: %q %q %q", turn1, turn2, other)
	}

	input := opencodeSession(none, []byte(`{"model":"m","input":[{"role":"user","content":"x"}]}`))
	str := opencodeSession(none, []byte(`{"model":"m","input":"x"}`))
	if !format.MatchString(input) || !format.MatchString(str) || input == str {
		t.Fatalf("responses input: %q %q", input, str)
	}

	r1, r2 := opencodeSession(none, []byte(`{"model":"m"}`)), opencodeSession(none, nil)
	if !format.MatchString(r1) || r1 == r2 {
		t.Fatalf("no message must get a fresh random id: %q %q", r1, r2)
	}

	if got := opencodeSession(http.Header{headerOpencodeSession: {" ses_mine "}}, nil); got != "ses_mine" {
		t.Fatalf("the client's own id must be kept: %q", got)
	}
	if got := opencodeSession(http.Header{headerOpencodeSession: {"bad\x01id"}}, nil); !format.MatchString(got) {
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
