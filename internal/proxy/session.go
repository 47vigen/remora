package proxy

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
func opencodeSession(h http.Header, body []byte) string {
	if v := strings.TrimSpace(h.Get(headerOpencodeSession)); v != "" && len(v) <= maxSessionLen && !strings.ContainsFunc(v, isControl) {
		return v
	}
	var sum [sha256.Size]byte
	if first := firstMessage(body); first != nil {
		sum = sha256.Sum256(append([]byte("opencode\x00ses_\x00"), first...))
	} else {
		_, _ = rand.Read(sum[:])
	}
	id := make([]byte, 14)
	for i, b := range sum[6:20] {
		id[i] = base62[int(b)%len(base62)]
	}
	return "ses_" + hex.EncodeToString(sum[:6]) + string(id)
}

// firstMessage is the first entry of a chat "messages" or a Responses "input" list, or a string input itself.
func firstMessage(body []byte) []byte {
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

func isControl(r rune) bool { return r < 0x20 || r == 0x7f }
