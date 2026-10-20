package mcp

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/47vigen/remora/internal/auth"
)

// listenHeartbeat paces the SSE comments on a subscriptions/listen stream. A stateless server never pushes on it, and
// a CDN in front answers 504 once a response stays silent for 100 s.
var listenHeartbeat = 20 * time.Second

// Handler serves /mcp: stateless Streamable HTTP with JSON responses, for API tokens only. The session cookie, the
// one credential a browser sends on its own, is dropped before authentication, so a request without a bearer API
// token is 401. The go-sdk's DNS-rebinding guard is off: it 403s a reverse proxy on the same host, and it protects
// unauthenticated servers, while every request here needs a token a rebinding page can neither read nor send.
func Handler(s *sdk.Server, a *auth.Service) http.Handler {
	sh := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return s },
		&sdk.StreamableHTTPOptions{Stateless: true, JSONResponse: true, DisableLocalhostProtection: true})
	h := a.Middleware(nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if msg, ok := readMessage(r); ok && msg.Method == "subscriptions/listen" {
			serveListen(w, r, msg)
			return
		}
		sh.ServeHTTP(w, r)
	}))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Del("Cookie")
		h.ServeHTTP(w, r)
	})
}

type message struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params struct {
		Notifications sdk.NotificationSubscriptions `json:"notifications"`
	} `json:"params"`
}

// maxPeek matches the go-sdk's own request body limit; a larger body is left whole for the go-sdk to refuse.
const maxPeek = 4 << 20

// readMessage peeks at a POST body and puts it back. A missing Mcp-Method header is filled from the body: Claude Code
// leaves it off notifications/cancelled, which the go-sdk then refuses with 400. A request that names its method in
// the header is read only when that method is subscriptions/listen.
func readMessage(r *http.Request) (message, bool) {
	var msg message
	if r.Method != http.MethodPost {
		return msg, false
	}
	if m := r.Header.Get("Mcp-Method"); m != "" && m != "subscriptions/listen" {
		return msg, false
	}
	b, err := io.ReadAll(io.LimitReader(r.Body, maxPeek+1))
	r.Body = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(b), r.Body), r.Body}
	if err != nil || len(b) > maxPeek || json.Unmarshal(b, &msg) != nil || msg.Method == "" {
		return msg, false
	}
	if r.Header.Get("Mcp-Method") == "" {
		r.Header.Set("Mcp-Method", msg.Method)
	}
	return msg, true
}

// serveListen acknowledges the subscription, as the first message must, then holds the stream open with comments until
// the client leaves. The stateless go-sdk handler writes nothing on it, so a client gives up waiting for the
// acknowledgement and reopens the stream. Only tools/list_changed is honoured, the one list_changed capability served.
func serveListen(w http.ResponseWriter, r *http.Request, msg message) {
	ack, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/subscriptions/acknowledged",
		"params": sdk.SubscriptionsAcknowledgedParams{
			Meta:          sdk.Meta{sdk.MetaKeySubscriptionID: msg.ID},
			Notifications: sdk.NotificationSubscriptions{ToolsListChanged: msg.Params.Notifications.ToolsListChanged},
		},
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.WriteHeader(http.StatusOK)
	if _, err := io.WriteString(w, "event: message\ndata: "+string(ack)+"\n\n"); err != nil {
		return
	}
	t := time.NewTicker(listenHeartbeat)
	defer t.Stop()
	for {
		if _, err := io.WriteString(w, ": keepalive\n\n"); err != nil || rc.Flush() != nil {
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-t.C:
		}
	}
}
