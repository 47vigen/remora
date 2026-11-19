package api

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	apispec "github.com/47vigen/remora/api"
	"github.com/47vigen/remora/internal/api/gen"
	"github.com/47vigen/remora/internal/auth"
	"github.com/47vigen/remora/internal/config"
	"github.com/47vigen/remora/internal/engine/bifrost"
	"github.com/47vigen/remora/internal/store"
)

// Deps are the services the management API runs on. A nil port answers 501 not_implemented for the operations
// that need it (remora.yaml export, plan and apply need Providers, Aliases, Keys and Batch; logs and stats need Logs).
type Deps struct {
	Providers Providers
	Accounts  Accounts // plan 03's accounts.Service, through NewAccounts
	Aliases   Aliases
	Keys      Keys
	Batch     config.Batch // joins the writes of one remora.yaml apply; reconcile.Reconciler.Batch
	System    System
	Names     EngineNames
	Logs      LogSource     // the front door's request log
	Quota     Quotas        // subscription accounts' quota, through NewQuota
	Tail      *bifrost.Tail // the one poller following it, fanned out to live log viewers
	Audit     *store.Store
	Auth      *auth.Service
	Log       *slog.Logger

	liveBuffer, liveBacklog int // a positive buffer overrides both live log queue sizes
}

// Server implements the management API's strict server interface.
type Server struct {
	d     Deps
	cfg   *config.Service  // remora.yaml over Providers, Aliases, Keys and Batch; nil while one is missing
	tests providerTests    // each provider's last test result
	now   func() time.Time // the clock for default time ranges
}

var _ gen.StrictServerInterface = (*Server)(nil)

// New returns a Server over d.
func New(d Deps) *Server {
	if d.liveBuffer <= 0 {
		d.liveBuffer, d.liveBacklog = liveBuffer, liveBacklog
	}
	s := &Server{d: d, now: time.Now}
	if d.Providers != nil && d.Aliases != nil && d.Keys != nil && d.Batch != nil {
		var as config.AccountStrategies
		if d.Accounts != nil {
			as = d.Accounts
		}
		s.cfg = config.NewService(d.Providers, d.Aliases, d.Keys, as, d.Batch)
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

// Handler serves /api/* behind the CSRF, authentication and scope checks. Mount it at "/api/".
func (s *Server) Handler() http.Handler {
	scopes, err := loadOperationScopes([]byte(apispec.OpenAPI))
	if err != nil {
		panic(err) // A bad embedded contract is a build error; do not start serving.
	}
	var middleware []gen.StrictMiddlewareFunc
	var httpMiddleware []gen.MiddlewareFunc
	if s.d.Audit != nil {
		middleware = append(middleware, s.auditOperations(scopes))
		httpMiddleware = append(httpMiddleware, auditResponses)
	}
	if s.d.Auth != nil {
		middleware = append(middleware, scopes.middleware)
	}
	mux := http.NewServeMux()
	strict := gen.NewStrictHandlerWithOptions(s, middleware, gen.StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  s.requestErrorHandler,
		ResponseErrorHandlerFunc: s.writeError,
	})
	gen.HandlerWithOptions(strict, gen.StdHTTPServerOptions{BaseRouter: mux, Middlewares: httpMiddleware, ErrorHandlerFunc: s.requestErrorHandler})
	// The live log WebSocket is hand-mounted and must authorize before upgrading.
	mux.HandleFunc("GET /api/logs/live", func(w http.ResponseWriter, r *http.Request) {
		if s.d.Auth == nil || scopes.authorize(w, r, "streamLogs") {
			s.streamLogs(w, r)
		}
	})
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
