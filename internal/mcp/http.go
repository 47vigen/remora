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
		if isListen(r) {
			serveListen(w, r)
			return
		}
		sh.ServeHTTP(w, r)
	}))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Del("Cookie")
		h.ServeHTTP(w, r)
	})
}

func isListen(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}
	if m := r.Header.Get("Mcp-Method"); m != "" {
		return m == "subscriptions/listen"
	}
	b, err := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewReader(b))
	var msg struct {
		Method string `json:"method"`
	}
	return err == nil && json.Unmarshal(b, &msg) == nil && msg.Method == "subscriptions/listen"
}

// serveListen holds the stream open with comments until the client leaves. The go-sdk would hold it silent: it sends
// no priming event from protocol 2026-07-28 on, so not even the headers leave.
func serveListen(w http.ResponseWriter, r *http.Request) {
	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.WriteHeader(http.StatusOK)
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
