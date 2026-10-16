package proxy

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
)

// headerOpencodeSession is OpenCode's conversation id. Its Go plan refuses a request without one, and both OpenCode
// plans route a conversation's turns together by it. The front door forwards an "x-bf-eh-<name>" header upstream as
// "<name>".
const (
	headerOpencodeSession = "X-Opencode-Session"
	headerForwardSession  = "X-Bf-Eh-X-Opencode-Session"
	maxSessionLen         = 256
)

const base62 = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// opencodeSession is the client's own session id when it sent a usable one. Otherwise it is derived from the
// conversation's first message, so every turn of one conversation carries the same id, in OpenCode's "ses_" format.
// A body with no message gets a random id.
func opencodeSession(h http.Header, body []byte, sp bodySpans) string {
	if v := strings.TrimSpace(h.Get(headerOpencodeSession)); v != "" && len(v) <= maxSessionLen && !strings.ContainsFunc(v, isControl) {
		return v
	}
	var sum [sha256.Size]byte
	if first := firstMessage(body, sp); first != nil {
		hash := sha256.New()
		_, _ = hash.Write([]byte("opencode\x00ses_\x00"))
		_, _ = hash.Write(first)
		hash.Sum(sum[:0])
	} else {
		_, _ = rand.Read(sum[:])
	}
	id := make([]byte, 14)
	for i, b := range sum[6:20] {
		id[i] = base62[int(b)%len(base62)]
	}
	return "ses_" + hex.EncodeToString(sum[:6]) + string(id)
}

// firstMessage is the first entry of a chat "messages" or a Responses "input" list, or a string input itself, read
// from the spans scanBody recorded. It answers exactly as decoding into
// struct{ Messages []json.RawMessage; Input json.RawMessage } did: a "messages" value that is not a list (or null)
// fails that decode, so it gives nil.
func firstMessage(body []byte, sp bodySpans) []byte {
	if sp.messagesNotList {
		return nil
	}
	first := func(arr span) []byte {
		var out []byte
		forEachElement(body, arr, func(el span) bool { out = body[el.start:el.end]; return false })
		return out
	}
	if v := sp.messages; v.end > 0 && body[v.start] == '[' {
		if m := first(v); m != nil {
			return m
		}
	}
	if v := sp.input; v.end > 0 {
		switch body[v.start] {
		case '[':
			return first(v)
		case '"':
			return body[v.start:v.end]
		}
	}
	return nil
}

func isControl(r rune) bool { return r < 0x20 || r == 0x7f }
