package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
)

type principalKey struct{}

// PrincipalFrom returns the principal Middleware authenticated, if any.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}

// Middleware guards /api/*. It first applies http.CrossOriginProtection, which refuses cross-origin unsafe
// requests by Sec-Fetch-Site or Origin (bearer clients send neither and pass), then authenticates every request
// for which public returns false. Failures answer with remora's JSON error body.
func (s *Service) Middleware(public func(*http.Request) bool) func(http.Handler) http.Handler {
	cop := http.NewCrossOriginProtection()
	cop.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		WriteError(w, http.StatusForbidden, "permission_error", "forbidden", "Cross-origin request refused.")
	}))
	return func(next http.Handler) http.Handler {
		authed := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if public != nil && public(r) {
				next.ServeHTTP(w, r)
				return
			}
			p, err := s.Authenticate(r)
			if err != nil {
				if errors.Is(err, ErrUnauthenticated) {
					WriteError(w, http.StatusUnauthorized, "authentication_error", "unauthorized", "Sign in or send an API token.")
				} else {
					WriteError(w, http.StatusInternalServerError, "api_error", "internal_error", "Internal error.")
				}
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey{}, p)))
		})
		return cop.Handler(authed)
	}
}

// errorBody is remora's error shape, the same as /v1's.
type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code"`
}

// WriteError writes remora's JSON error body.
func WriteError(w http.ResponseWriter, status int, typ, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorBody{Error: errorDetail{Message: message, Type: typ, Code: code}})
}
