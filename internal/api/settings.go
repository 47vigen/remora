package api

import (
	"context"

	"github.com/47vigen/remora/internal/api/gen"
	"github.com/47vigen/remora/internal/auth"
	"github.com/47vigen/remora/internal/store"
)

// ChangePassword changes the admin password. Only a signed-in session may: an API token is refused with 403, so
// a leaked token cannot lock the admin out. Every other session is ended; the caller's stays valid.
func (s *Server) ChangePassword(ctx context.Context, req gen.ChangePasswordRequestObject) (gen.ChangePasswordResponseObject, error) {
	r := requestFrom(ctx)
	if s.d.Auth == nil || r == nil {
		return nil, errNotImplemented
	}
	if p, ok := auth.PrincipalFrom(ctx); !ok || p.Via != "session" {
		return nil, errForbidden
	}
	if req.Body == nil || req.Body.Current == "" || req.Body.New == "" {
		return nil, badRequest("current and new are required")
	}
	if err := s.d.Auth.ChangePassword(ctx, auth.SessionToken(r), req.Body.Current, req.Body.New); err != nil {
		return nil, err
	}
	return gen.ChangePassword204Response{}, nil
}

func toGenToken(t store.APIToken) gen.ApiToken {
	return gen.ApiToken{Id: t.ID, Name: t.Name, Prefix: t.Prefix, CreatedAt: t.CreatedAt, LastUsedAt: t.LastUsedAt}
}

// ListApiTokens lists the API tokens, searched by name or prefix, one page at a time. Values are never shown.
func (s *Server) ListApiTokens(ctx context.Context, req gen.ListApiTokensRequestObject) (gen.ListApiTokensResponseObject, error) { //nolint:revive // the generated interface names it
	if s.d.Auth == nil {
		return nil, errNotImplemented
	}
	all, err := s.d.Auth.ListTokens(ctx)
	if err != nil {
		return nil, err
	}
	var out []gen.ApiToken
	for _, t := range all {
		if matches(req.Params.Q, t.Name, t.Prefix) {
			out = append(out, toGenToken(t))
		}
	}
	items, total := page(out, req.Params.Limit, req.Params.Offset)
	return gen.ListApiTokens200JSONResponse{Items: items, Total: total}, nil
}

// CreateApiToken creates an API token; its value ("rmt_…") is answered only here.
func (s *Server) CreateApiToken(ctx context.Context, req gen.CreateApiTokenRequestObject) (gen.CreateApiTokenResponseObject, error) { //nolint:revive // the generated interface names it
	if s.d.Auth == nil {
		return nil, errNotImplemented
	}
	if req.Body == nil || req.Body.Name == "" {
		return nil, badRequest("a token needs a name")
	}
	t, value, err := s.d.Auth.CreateToken(ctx, req.Body.Name)
	if err != nil {
		return nil, err
	}
	g := toGenToken(t)
	return gen.CreateApiToken201JSONResponse{
		Id: g.Id, Name: g.Name, Prefix: g.Prefix, CreatedAt: g.CreatedAt, LastUsedAt: g.LastUsedAt, Value: value,
	}, nil
}

// DeleteApiToken revokes an API token at once.
func (s *Server) DeleteApiToken(ctx context.Context, req gen.DeleteApiTokenRequestObject) (gen.DeleteApiTokenResponseObject, error) { //nolint:revive // the generated interface names it
	if s.d.Auth == nil {
		return nil, errNotImplemented
	}
	if err := s.d.Auth.DeleteToken(ctx, req.Id); err != nil {
		return nil, err
	}
	return gen.DeleteApiToken204Response{}, nil
}
