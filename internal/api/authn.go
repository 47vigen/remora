package api

import (
	"context"
	"net"
	"net/http"

	"github.com/47vigen/remora/internal/api/gen"
	"github.com/47vigen/remora/internal/auth"
)

// Login checks the admin password and sets the session cookie.
func (s *Server) Login(ctx context.Context, req gen.LoginRequestObject) (gen.LoginResponseObject, error) {
	r := requestFrom(ctx)
	if s.d.Auth == nil || r == nil {
		return nil, errNotImplemented
	}
	if req.Body == nil || req.Body.Password == "" {
		return nil, badRequest("password is required")
	}
	token, expires, err := s.d.Auth.Login(ctx, remoteIP(r), req.Body.Password)
	if err != nil {
		return nil, err
	}
	cookie := auth.SessionCookie(r, token, expires).String()
	return gen.Login204Response{Headers: gen.Login204ResponseHeaders{SetCookie: &cookie}}, nil
}

// Logout ends the caller's session, if it has one, and clears the cookie.
func (s *Server) Logout(ctx context.Context, _ gen.LogoutRequestObject) (gen.LogoutResponseObject, error) {
	r := requestFrom(ctx)
	if s.d.Auth == nil || r == nil {
		return nil, errNotImplemented
	}
	if err := s.d.Auth.Logout(ctx, auth.SessionToken(r)); err != nil {
		return nil, err
	}
	cookie := auth.ClearedSessionCookie(r).String()
	return gen.Logout204Response{Headers: gen.Logout204ResponseHeaders{SetCookie: &cookie}}, nil
}

// GetSession reports how the caller is authenticated.
func (s *Server) GetSession(ctx context.Context, _ gen.GetSessionRequestObject) (gen.GetSessionResponseObject, error) {
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return nil, auth.ErrUnauthenticated
	}
	return gen.GetSession200JSONResponse{Authenticated: true, Via: gen.SessionVia(p.Via)}, nil
}

// remoteIP is the client's IP as the listener saw it. Forwarding headers are not trusted: a client could set
// them to escape the login limiter.
func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
