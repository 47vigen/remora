package api

import (
	"context"
	"fmt"
	"path"

	"github.com/47vigen/remora/internal/api/gen"
)

// toGenKey maps a key to its answer. The value is never part of it; allow is never null.
func toGenKey(k Key) gen.Key {
	return gen.Key{
		Id: k.ID, Name: k.Name, Prefix: k.Prefix, Allow: append([]string{}, k.Allow...), Enabled: k.Enabled,
		CreatedAt: k.CreatedAt,
	}
}

// validAllow checks an allowlist: every entry is a non-empty alias name or glob that path.Match accepts. An empty
// list is valid and denies everything.
func validAllow(allow []string) error {
	for _, p := range allow {
		if p == "" {
			return badRequest("an allow entry cannot be empty")
		}
		if _, err := path.Match(p, ""); err != nil {
			return badRequest(fmt.Sprintf("allow entry %q is not a valid pattern", p))
		}
	}
	return nil
}

// ListKeys lists client keys, searched by name or prefix, one page at a time.
func (s *Server) ListKeys(ctx context.Context, req gen.ListKeysRequestObject) (gen.ListKeysResponseObject, error) {
	if s.d.Keys == nil {
		return nil, errNotImplemented
	}
	all, err := s.d.Keys.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []gen.Key
	for _, k := range all {
		if matches(req.Params.Q, k.Name, k.Prefix) {
			out = append(out, toGenKey(k))
		}
	}
	items, total := page(out, req.Params.Limit, req.Params.Offset)
	return gen.ListKeys200JSONResponse{Items: items, Total: total}, nil
}

// CreateKey creates a client key, with a generated value unless the caller chose one. The value is answered only
// here.
func (s *Server) CreateKey(ctx context.Context, req gen.CreateKeyRequestObject) (gen.CreateKeyResponseObject, error) {
	if s.d.Keys == nil {
		return nil, errNotImplemented
	}
	b := req.Body
	if b == nil {
		return nil, badRequest("a key body is required")
	}
	if b.Name == "" {
		return nil, badRequest("a key needs a name")
	}
	if err := validAllow(b.Allow); err != nil {
		return nil, err
	}
	if b.Value != nil && *b.Value == "" {
		return nil, badRequest("value cannot be empty; leave it out to generate one")
	}
	allow := append([]string{}, b.Allow...)
	k, value, err := s.d.Keys.Create(ctx, b.Name, allow, deref(b.Value), true)
	if err != nil {
		return nil, err
	}
	g := toGenKey(k)
	return gen.CreateKey201JSONResponse{
		Id: g.Id, Name: g.Name, Prefix: g.Prefix, Allow: g.Allow, Enabled: g.Enabled, CreatedAt: g.CreatedAt,
		Value: value,
	}, nil
}

// GetKey returns a client key, without its value.
func (s *Server) GetKey(ctx context.Context, req gen.GetKeyRequestObject) (gen.GetKeyResponseObject, error) {
	if s.d.Keys == nil {
		return nil, errNotImplemented
	}
	k, err := s.d.Keys.Get(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	return gen.GetKey200JSONResponse(toGenKey(k)), nil
}

// UpdateKey renames a key, replaces its allowlist or enables or disables it; fields left out are kept. An empty
// allowlist denies everything.
func (s *Server) UpdateKey(ctx context.Context, req gen.UpdateKeyRequestObject) (gen.UpdateKeyResponseObject, error) {
	if s.d.Keys == nil {
		return nil, errNotImplemented
	}
	b := req.Body
	if b == nil {
		return nil, badRequest("a key update body is required")
	}
	if b.Name != nil && *b.Name == "" {
		return nil, badRequest("a key needs a name")
	}
	p := KeyPatch{Name: b.Name, Enabled: b.Enabled}
	if b.Allow != nil {
		if err := validAllow(*b.Allow); err != nil {
			return nil, err
		}
		allow := append([]string{}, *b.Allow...)
		p.Allow = &allow
	}
	k, err := s.d.Keys.Update(ctx, req.Id, p)
	if err != nil {
		return nil, err
	}
	return gen.UpdateKey200JSONResponse(toGenKey(k)), nil
}

// DeleteKey deletes a client key.
func (s *Server) DeleteKey(ctx context.Context, req gen.DeleteKeyRequestObject) (gen.DeleteKeyResponseObject, error) {
	if s.d.Keys == nil {
		return nil, errNotImplemented
	}
	if err := s.d.Keys.Delete(ctx, req.Id); err != nil {
		return nil, err
	}
	return gen.DeleteKey204Response{}, nil
}
