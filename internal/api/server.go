package api

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/47vigen/remora/internal/api/gen"
	"github.com/47vigen/remora/internal/auth"
	"github.com/47vigen/remora/internal/config"
)

// Deps are the services the management API runs on. A nil port answers 501 not_implemented for the operations
// that need it (remora.yaml export, plan and apply need Providers, Aliases and Keys; logs and stats need Logs).
type Deps struct {
	Providers Providers
	Accounts  Accounts // plan 03's accounts.Service, through NewAccounts
	Aliases   Aliases
	Keys      Keys
	System    System
	Names     EngineNames
	Logs      LogSource // the front door's request log
	Auth      *auth.Service
	Log       *slog.Logger
}

// Server implements the management API's strict server interface.
type Server struct {
	d     Deps
	cfg   *config.Service  // remora.yaml over Providers, Aliases and Keys; nil while one is missing
	tests providerTests    // each provider's last test result
	now   func() time.Time // the clock for default time ranges
}

var _ gen.StrictServerInterface = (*Server)(nil)

// New returns a Server over d.
func New(d Deps) *Server {
	s := &Server{d: d, now: time.Now}
	if d.Providers != nil && d.Aliases != nil && d.Keys != nil {
		s.cfg = config.NewService(d.Providers, d.Aliases, d.Keys)
	}
	return s
}

func (s *Server) log() *slog.Logger {
	if s.d.Log == nil {
		return slog.New(slog.DiscardHandler)
	}
	return s.d.Log
}

// publicPaths answer without authentication: the operations the contract marks `security: []`.
// /api/system/version stays public as plan 01 shipped it (the foundation e2e reads it anonymously).
var publicPaths = map[string]bool{
	"GET /api/health":         true,
	"POST /api/auth/login":    true,
	"GET /api/system/version": true,
}

func isPublic(r *http.Request) bool { return publicPaths[r.Method+" "+r.URL.Path] }

// Handler serves /api/*: the generated routes behind the CSRF guard and admin authentication. Mount it at "/api/".
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	strict := gen.NewStrictHandlerWithOptions(s, nil, gen.StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  s.requestErrorHandler,
		ResponseErrorHandlerFunc: s.writeError,
	})
	gen.HandlerWithOptions(strict, gen.StdHTTPServerOptions{BaseRouter: mux, ErrorHandlerFunc: s.requestErrorHandler})
	mux.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) {
		auth.WriteError(w, http.StatusNotFound, typeNotFound, "not_found", "Unknown path or method.")
	})

	var h http.Handler = mux
	if s.d.Auth != nil {
		h = s.d.Auth.Middleware(isPublic)(h)
	}
	return withRequest(noStore(h))
}

// noStore keeps management answers out of caches.
func noStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

type requestKey struct{}

// withRequest puts the request into its own context, for strict handlers that need the request itself (the
// login cookie's Secure flag, the client IP).
func withRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestKey{}, r)))
	})
}

// requestFrom returns the request withRequest stored, or nil.
func requestFrom(ctx context.Context) *http.Request {
	r, _ := ctx.Value(requestKey{}).(*http.Request)
	return r
}
