package api

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/47vigen/remora/internal/alias"
	"github.com/47vigen/remora/internal/api/gen"
	"github.com/47vigen/remora/internal/engine/cliproxyapi"
)

// toGenAlias maps an alias to its answer. Lists are never null.
func toGenAlias(a alias.Alias) gen.Alias {
	members := make([]string, 0, len(a.Members))
	for _, m := range a.Members {
		members = append(members, m.Ref)
	}
	out := gen.Alias{
		Name: a.Name, Kind: gen.AliasKind(a.Kind), Members: members,
		Capabilities: append([]string{}, a.Capabilities...),
	}
	if a.ContextWindow > 0 {
		cw := a.ContextWindow
		out.ContextWindow = &cw
	}
	st := gen.RoutingStrategy(cmp.Or(a.Strategy, alias.StrategyPriority))
	out.Strategy = &st
	return out
}

// fromGenAlias validates a request body and maps it to an alias. Whether the members resolve (no cycle, not too
// deep, no unknown alias) is the Aliases port's check.
func fromGenAlias(b *gen.Alias) (alias.Alias, error) {
	if b == nil {
		return alias.Alias{}, badRequest("an alias body is required")
	}
	if err := alias.ValidName(b.Name); err != nil {
		return alias.Alias{}, badRequest(fmt.Sprintf("invalid alias name %q: use letters, digits and . _ : / -, up to 128 characters", b.Name))
	}
	if !b.Kind.Valid() {
		return alias.Alias{}, badRequest(fmt.Sprintf("kind must be one of chat, embeddings, transcription, decisions; got %q", b.Kind))
	}
	if len(b.Members) == 0 {
		return alias.Alias{}, badRequest("an alias needs at least one member")
	}
	a := alias.Alias{Name: b.Name, Kind: alias.Kind(b.Kind), Capabilities: b.Capabilities}
	for _, m := range b.Members {
		if m == "" {
			return alias.Alias{}, badRequest("a member cannot be empty")
		}
		if err := alias.CheckThinking(m, cliproxyapi.IsSubscription); err != nil {
			return alias.Alias{}, badRequest(err.Error())
		}
		a.Members = append(a.Members, alias.Member{Ref: m})
	}
	if b.ContextWindow != nil {
		if *b.ContextWindow < 0 {
			return alias.Alias{}, badRequest("context_window cannot be negative")
		}
		a.ContextWindow = *b.ContextWindow
	}
	a.Strategy = alias.StrategyPriority
	if b.Strategy != nil {
		if a.Strategy = alias.Strategy(*b.Strategy); !a.Strategy.Valid() {
			return alias.Alias{}, badStrategy(a.Strategy)
		}
	}
	return a, nil
}

func badStrategy(st alias.Strategy) error {
	return badRequest(fmt.Sprintf("strategy must be one of priority, fill-first, round-robin, random, p2c, least-used; got %q", st))
}

// coolingUntil maps each cooling target to when it cools down (the latest, should a target be listed twice).
func (s *Server) coolingUntil(ctx context.Context) (map[alias.Target]time.Time, error) {
	cooling, err := s.d.Aliases.Cooling(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[alias.Target]time.Time, len(cooling))
	for _, c := range cooling {
		if c.Until.After(out[c.Target]) {
			out[c.Target] = c.Until
		}
	}
	return out, nil
}

// aliasesByTarget maps every target a stored alias reaches to the aliases (by name, in list order) whose chain
// reaches it. An alias that does not compile reaches nothing.
func (s *Server) aliasesByTarget(ctx context.Context) (map[alias.Target][]string, error) {
	all, err := s.d.Aliases.List(ctx)
	if err != nil {
		return nil, err
	}
	out := map[alias.Target][]string{}
	for _, a := range all {
		chain, err := s.d.Aliases.Chain(ctx, a.Name)
		if err != nil {
			continue
		}
		for _, t := range chain {
			out[t] = append(out[t], a.Name)
		}
	}
	return out, nil
}

// toChain maps compiled targets to a chain, marking the cooling ones. The targets list is never null.
func toChain(targets []alias.Target, cooling map[alias.Target]time.Time) gen.AliasChain {
	out := gen.AliasChain{Targets: make([]gen.ChainTarget, 0, len(targets))}
	for _, t := range targets {
		ct := gen.ChainTarget{Provider: t.Provider, Model: t.Model}
		if until, ok := cooling[t]; ok {
			ct.CoolingUntil = &until
		}
		out.Targets = append(out.Targets, ct)
	}
	return out
}

// ListAliases lists aliases, filtered by kind and a name search, one page at a time.
func (s *Server) ListAliases(ctx context.Context, req gen.ListAliasesRequestObject) (gen.ListAliasesResponseObject, error) {
	if s.d.Aliases == nil {
		return nil, errNotImplemented
	}
	pr := req.Params
	if pr.Kind != nil && !pr.Kind.Valid() {
		return nil, badRequest(fmt.Sprintf("unknown alias kind %q", *pr.Kind))
	}
	all, err := s.d.Aliases.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []gen.Alias
	for _, a := range all {
		if pr.Kind != nil && string(*pr.Kind) != string(a.Kind) || !matches(pr.Q, a.Name) {
			continue
		}
		out = append(out, toGenAlias(a))
	}
	items, total := page(out, pr.Limit, pr.Offset)
	return gen.ListAliases200JSONResponse{Items: items, Total: total}, nil
}

// CreateAlias creates an alias.
func (s *Server) CreateAlias(ctx context.Context, req gen.CreateAliasRequestObject) (gen.CreateAliasResponseObject, error) {
	if s.d.Aliases == nil {
		return nil, errNotImplemented
	}
	a, err := fromGenAlias(req.Body)
	if err != nil {
		return nil, err
	}
	if a, err = s.d.Aliases.Put(ctx, a, true); err != nil {
		return nil, err
	}
	return gen.CreateAlias201JSONResponse(toGenAlias(a)), nil
}

// GetAlias returns an alias.
func (s *Server) GetAlias(ctx context.Context, req gen.GetAliasRequestObject) (gen.GetAliasResponseObject, error) {
	if s.d.Aliases == nil {
		return nil, errNotImplemented
	}
	a, err := s.d.Aliases.Get(ctx, req.Name)
	if err != nil {
		return nil, err
	}
	return gen.GetAlias200JSONResponse(toGenAlias(a)), nil
}

// UpdateAlias replaces an alias. An alias cannot be renamed.
func (s *Server) UpdateAlias(ctx context.Context, req gen.UpdateAliasRequestObject) (gen.UpdateAliasResponseObject, error) {
	if s.d.Aliases == nil {
		return nil, errNotImplemented
	}
	if req.Body != nil && req.Body.Name == "" {
		req.Body.Name = req.Name
	}
	a, err := fromGenAlias(req.Body)
	if err != nil {
		return nil, err
	}
	if a.Name != req.Name {
		return nil, badRequest("an alias cannot be renamed; the body's name must match the path")
	}
	if a, err = s.d.Aliases.Put(ctx, a, false); err != nil {
		return nil, err
	}
	return gen.UpdateAlias200JSONResponse(toGenAlias(a)), nil
}

// DeleteAlias deletes an alias.
func (s *Server) DeleteAlias(ctx context.Context, req gen.DeleteAliasRequestObject) (gen.DeleteAliasResponseObject, error) {
	if s.d.Aliases == nil {
		return nil, errNotImplemented
	}
	if err := s.d.Aliases.Delete(ctx, req.Name); err != nil {
		return nil, err
	}
	return gen.DeleteAlias204Response{}, nil
}

// GetAliasChain returns a stored alias's compiled chain, each cooling target with the time it cools down.
func (s *Server) GetAliasChain(ctx context.Context, req gen.GetAliasChainRequestObject) (gen.GetAliasChainResponseObject, error) {
	if s.d.Aliases == nil {
		return nil, errNotImplemented
	}
	targets, err := s.d.Aliases.Chain(ctx, req.Name)
	if err != nil {
		return nil, err
	}
	cooling, err := s.coolingUntil(ctx)
	if err != nil {
		return nil, err
	}
	return gen.GetAliasChain200JSONResponse(toChain(targets, cooling)), nil
}

// CompileAlias compiles an unsaved alias against the stored ones; nothing is stored.
func (s *Server) CompileAlias(ctx context.Context, req gen.CompileAliasRequestObject) (gen.CompileAliasResponseObject, error) {
	if s.d.Aliases == nil {
		return nil, errNotImplemented
	}
	a, err := fromGenAlias(req.Body)
	if err != nil {
		return nil, err
	}
	targets, err := s.d.Aliases.Compile(ctx, a)
	if err != nil {
		return nil, err
	}
	cooling, err := s.coolingUntil(ctx)
	if err != nil {
		return nil, err
	}
	return gen.CompileAlias200JSONResponse(toChain(targets, cooling)), nil
}

// ListCooling lists the cooling targets, each with the aliases whose chain reaches it, one page at a time. The
// search matches the provider, the model, "<provider>/<model>", the reason and the alias names.
func (s *Server) ListCooling(ctx context.Context, req gen.ListCoolingRequestObject) (gen.ListCoolingResponseObject, error) {
	if s.d.Aliases == nil {
		return nil, errNotImplemented
	}
	cooling, err := s.d.Aliases.Cooling(ctx)
	if err != nil {
		return nil, err
	}
	var usedBy map[alias.Target][]string
	if len(cooling) > 0 {
		if usedBy, err = s.aliasesByTarget(ctx); err != nil {
			return nil, err
		}
	}
	out := make([]gen.Cooling, 0, len(cooling))
	for _, c := range cooling {
		names := slices.Sorted(slices.Values(usedBy[c.Target]))
		if names == nil {
			names = []string{}
		}
		fields := append([]string{c.Target.Provider, c.Target.Model, c.Target.Provider + "/" + c.Target.Model, c.Reason}, names...)
		if !matches(req.Params.Q, fields...) {
			continue
		}
		out = append(out, gen.Cooling{
			Provider: c.Target.Provider, Model: c.Target.Model, Until: c.Until, Reason: c.Reason, Aliases: names,
		})
	}
	slices.SortFunc(out, func(a, b gen.Cooling) int {
		return cmp.Or(a.Until.Compare(b.Until), cmp.Compare(a.Provider, b.Provider), cmp.Compare(a.Model, b.Model))
	})
	items, total := page(out, req.Params.Limit, req.Params.Offset)
	return gen.ListCooling200JSONResponse{Items: items, Total: total}, nil
}
