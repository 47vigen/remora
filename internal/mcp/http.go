package mcp

import (
	"net/http"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/47vigen/remora/internal/auth"
)

// Handler serves /mcp: stateless Streamable HTTP with JSON responses, for API tokens only. The session cookie, the
// one credential a browser sends on its own, is dropped before authentication, so a request without a bearer API
// token is 401. The go-sdk's DNS-rebinding guard is off: it 403s a reverse proxy on the same host, and it protects
// unauthenticated servers, while every request here needs a token a rebinding page can neither read nor send.
func Handler(s *sdk.Server, a *auth.Service) http.Handler {
	h := a.Middleware(nil)(sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return s },
		&sdk.StreamableHTTPOptions{Stateless: true, JSONResponse: true, DisableLocalhostProtection: true}))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Del("Cookie")
		h.ServeHTTP(w, r)
	})
}
