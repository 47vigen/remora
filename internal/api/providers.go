package api

import (
	"context"
	"fmt"
	"regexp"
	"sync"

	"github.com/47vigen/remora/internal/alias"
	"github.com/47vigen/remora/internal/api/gen"
)

// providerNamePattern: provider names prefix "<provider>/<model>" refs, so they are lower-case slugs.
var providerNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// providerTests remembers each provider's last test result, in memory: a restart forgets them and the status
// goes back to "unknown".
type providerTests struct {
	mu sync.Mutex
	ok map[string]bool
}

func (p *providerTests) set(name string, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ok == nil {
		p.ok = map[string]bool{}
	}
	p.ok[name] = ok
}

func (p *providerTests) forget(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.ok, name)
}

func (p *providerTests) get(name string) (ok, tested bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	ok, tested = p.ok[name]
	return ok, tested
}

// providerHealth is what a provider's status is computed from: the models aliases reach on each provider, and
// the targets cooling now.
type providerHealth struct {
	models  map[string]map[string]bool // provider → model → cooling
	tests   *providerTests
	enabled bool
}

// health collects the alias targets and the cooling snapshot. Without an Aliases port, status rests on tests only.
func (s *Server) health(ctx context.Context) (providerHealth, error) {
	h := providerHealth{models: map[string]map[string]bool{}, tests: &s.tests}
	if s.d.Aliases == nil {
		return h, nil
	}
	cooling, err := s.d.Aliases.Cooling(ctx)
	if err != nil {
		return h, err
	}
	if len(cooling) == 0 {
		return h, nil // nothing cooling: the models do not matter
	}
	h.enabled = true
	cool := map[alias.Target]bool{}
	for _, c := range cooling {
		cool[c.Target] = true
	}
	all, err := s.d.Aliases.List(ctx)
	if err != nil {
		return h, err
	}
	for _, a := range all {
		chain, err := s.d.Aliases.Chain(ctx, a.Name)
		if err != nil {
			continue // an alias that does not compile reaches nothing
		}
		for _, t := range chain {
			if h.models[t.Provider] == nil {
				h.models[t.Provider] = map[string]bool{}
			}
			h.models[t.Provider][t.Model] = cool[t]
		}
	}
	return h, nil
}

// status: "down" when the last test failed or every model the aliases reach on it is cooling, "degraded" when
// some are, else "ok" after a passing test and "unknown" before the first.
func (h providerHealth) status(name string) gen.ProviderStatus {
	ok, tested := h.tests.get(name)
	if tested && !ok {
		return gen.ProviderStatusDown
	}
	if h.enabled {
		models, cooling := h.models[name], 0
		for _, c := range models {
			if c {
				cooling++
			}
		}
		switch {
		case cooling > 0 && cooling == len(models):
			return gen.ProviderStatusDown
		case cooling > 0:
			return gen.ProviderStatusDegraded
		}
	}
	if tested {
		return gen.ProviderStatusOk
	}
	return gen.ProviderStatusUnknown
}

// toGenProvider maps a provider to its answer: key names only, never a value.
func toGenProvider(p Provider, h providerHealth) gen.Provider {
	keys := make([]gen.ProviderKey, 0, len(p.Keys))
	for _, k := range p.Keys {
		keys = append(keys, gen.ProviderKey{Name: k.Name})
	}
	n, st, apn := len(keys), h.status(p.Name), p.AllowPrivateNetwork
	out := gen.Provider{
		Name: p.Name, Kind: gen.ProviderKind(p.Kind), Keys: &keys, KeyCount: &n, Status: &st,
		AllowPrivateNetwork: &apn,
	}
	if p.Type != "" {
		out.Type = &p.Type
	}
	if p.BaseURL != "" {
		out.BaseUrl = &p.BaseURL
	}
	return out
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

// fromGenProvider validates a request body and maps it to a provider. On create every key needs a value; on
// update a key without one keeps its stored secret, and no keys at all keep every stored key.
func fromGenProvider(b *gen.Provider, create bool) (Provider, error) {
	if b == nil {
		return Provider{}, badRequest("a provider body is required")
	}
	if !providerNamePattern.MatchString(b.Name) {
		return Provider{}, badRequest("name must be 1 to 63 lower-case letters, digits or '-', starting with a letter or digit")
	}
	if !b.Kind.Valid() {
		return Provider{}, badRequest(fmt.Sprintf("kind must be one of builtin, openai-compatible, typesafe-compatible, soniox; got %q", b.Kind))
	}
	p := Provider{
		Name: b.Name, Kind: string(b.Kind), Type: deref(b.Type), BaseURL: deref(b.BaseUrl),
		AllowPrivateNetwork: deref(b.AllowPrivateNetwork),
	}
	switch {
	case b.Kind == gen.Builtin && p.Type == "":
		return Provider{}, badRequest("a builtin provider needs a type")
	case b.Kind != gen.Builtin && p.Type != "":
		return Provider{}, badRequest("type is only for builtin providers")
	case (b.Kind == gen.OpenaiCompatible || b.Kind == gen.TypesafeCompatible) && p.BaseURL == "":
		return Provider{}, badRequest(fmt.Sprintf("a %s provider needs a base_url", b.Kind))
	}
	seen := map[string]bool{}
	for _, k := range deref(b.Keys) {
		v := deref(k.Value)
		switch {
		case k.Name == "":
			return Provider{}, badRequest("every key needs a name")
		case seen[k.Name]:
			return Provider{}, badRequest(fmt.Sprintf("key %q appears twice", k.Name))
		case create && v == "":
			return Provider{}, badRequest(fmt.Sprintf("key %q needs a value", k.Name))
		}
		seen[k.Name] = true
		p.Keys = append(p.Keys, ProviderKey{Name: k.Name, Value: v})
	}
	return p, nil
}

// ListProviders lists providers, filtered by kind, type and a name search, one page at a time.
func (s *Server) ListProviders(ctx context.Context, req gen.ListProvidersRequestObject) (gen.ListProvidersResponseObject, error) {
	if s.d.Providers == nil {
		return nil, errNotImplemented
	}
	all, err := s.d.Providers.List(ctx)
	if err != nil {
		return nil, err
	}
	h, err := s.health(ctx)
	if err != nil {
		return nil, err
	}
	pr := req.Params
	var out []gen.Provider
	for _, p := range all {
		if pr.Kind != nil && string(*pr.Kind) != p.Kind || pr.Type != nil && *pr.Type != p.Type || !matches(pr.Q, p.Name) {
			continue
		}
		out = append(out, toGenProvider(p, h))
	}
	items, total := page(out, pr.Limit, pr.Offset)
	return gen.ListProviders200JSONResponse{Items: items, Total: total}, nil
}

// CreateProvider creates a provider.
func (s *Server) CreateProvider(ctx context.Context, req gen.CreateProviderRequestObject) (gen.CreateProviderResponseObject, error) {
	if s.d.Providers == nil {
		return nil, errNotImplemented
	}
	p, err := fromGenProvider(req.Body, true)
	if err != nil {
		return nil, err
	}
	if p, err = s.d.Providers.Create(ctx, p); err != nil {
		return nil, err
	}
	s.tests.forget(p.Name)
	h, err := s.health(ctx)
	if err != nil {
		return nil, err
	}
	return gen.CreateProvider201JSONResponse(toGenProvider(p, h)), nil
}

// GetProvider returns a provider.
func (s *Server) GetProvider(ctx context.Context, req gen.GetProviderRequestObject) (gen.GetProviderResponseObject, error) {
	if s.d.Providers == nil {
		return nil, errNotImplemented
	}
	p, err := s.d.Providers.Get(ctx, req.Name)
	if err != nil {
		return nil, err
	}
	h, err := s.health(ctx)
	if err != nil {
		return nil, err
	}
	return gen.GetProvider200JSONResponse(toGenProvider(p, h)), nil
}

// UpdateProvider replaces a provider. Keys sent without a value keep their stored secret; no keys keep them all.
func (s *Server) UpdateProvider(ctx context.Context, req gen.UpdateProviderRequestObject) (gen.UpdateProviderResponseObject, error) {
	if s.d.Providers == nil {
		return nil, errNotImplemented
	}
	if req.Body != nil && req.Body.Name == "" {
		req.Body.Name = req.Name
	}
	p, err := fromGenProvider(req.Body, false)
	if err != nil {
		return nil, err
	}
	if p.Name != req.Name {
		return nil, badRequest("a provider cannot be renamed; the body's name must match the path")
	}
	if p, err = s.d.Providers.Update(ctx, p); err != nil {
		return nil, err
	}
	h, err := s.health(ctx)
	if err != nil {
		return nil, err
	}
	return gen.UpdateProvider200JSONResponse(toGenProvider(p, h)), nil
}

// DeleteProvider deletes a provider.
func (s *Server) DeleteProvider(ctx context.Context, req gen.DeleteProviderRequestObject) (gen.DeleteProviderResponseObject, error) {
	if s.d.Providers == nil {
		return nil, errNotImplemented
	}
	if err := s.d.Providers.Delete(ctx, req.Name); err != nil {
		return nil, err
	}
	s.tests.forget(req.Name)
	return gen.DeleteProvider204Response{}, nil
}

// TestProvider sends a small request to the provider and remembers whether it passed, for the provider's status.
// A failing upstream is a 200 answer with ok false; only a failure to run the test is an error.
func (s *Server) TestProvider(ctx context.Context, req gen.TestProviderRequestObject) (gen.TestProviderResponseObject, error) {
	if s.d.Providers == nil {
		return nil, errNotImplemented
	}
	r, err := s.d.Providers.Test(ctx, req.Name)
	if err != nil {
		return nil, err
	}
	s.tests.set(req.Name, r.OK)
	return gen.TestProvider200JSONResponse{Ok: r.OK, LatencyMs: int(r.Latency.Milliseconds()), Message: r.Message}, nil
}
