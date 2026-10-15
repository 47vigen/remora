package config

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/47vigen/remora/internal/alias"
)

// ErrInvalid marks a document that cannot be planned against the store, such as a new resource whose secret is
// not set. Its text is meant for the caller.
var ErrInvalid = errors.New("remora.yaml")

// Change is one difference between a document and the store. Op is "create", "update" or "delete"; Kind is
// "provider", "alias" or "key"; Fields lists the fields an update changes ("members", "allow", "keys", "base_url",
// ...) and is empty, not nil, for create and delete. Value is set only on a client key that Apply created with a
// generated value, so that it can be shown once.
type Change struct {
	Op, Kind, Name string
	Fields         []string
	Value          string
}

// Service plans and applies remora.yaml documents and exports the store as one, through the management ports.
type Service struct {
	p     Providers
	a     Aliases
	k     Keys
	batch Batch
}

// NewService returns a Service over the provider, alias and client key ports; Apply makes its writes in one batch.
func NewService(p Providers, a Aliases, k Keys, batch Batch) *Service {
	return &Service{p: p, a: a, k: k, batch: batch}
}

// step is a planned change and the port calls that make it. run returns a generated client key value, if any.
type step struct {
	Change
	run func(ctx context.Context) (string, error)
}

// Plan returns the changes that would make the store match want, in the order Apply makes them; it changes
// nothing. Providers are matched by name, provider keys by provider and key name, aliases and client keys by name.
// A secret "env:NAME" is sec[NAME]: when NAME is absent the stored secret is kept, and a resource that does not
// exist yet is an ErrInvalid error naming it. With prune, what the store has and want does not is deleted;
// without it nothing is deleted, not even a provider key want leaves out.
func (s *Service) Plan(ctx context.Context, want File, sec Secrets, prune bool) ([]Change, error) {
	steps, err := s.plan(ctx, want, sec, prune)
	if err != nil {
		return nil, err
	}
	out := make([]Change, len(steps))
	for i, st := range steps {
		out[i] = st.Change
	}
	return out, nil
}

// plan orders the steps: providers, aliases with members first, client keys; then, with prune, deletions in
// reverse: client keys, aliases with dependents first, providers.
func (s *Service) plan(ctx context.Context, want File, sec Secrets, prune bool) ([]step, error) {
	if err := want.Validate(); err != nil {
		return nil, err
	}
	var steps []step
	providers, err := s.providerSteps(ctx, want, sec, prune)
	if err != nil {
		return nil, err
	}
	steps = append(steps, providers...)
	aliases, err := s.aliasSteps(ctx, want)
	if err != nil {
		return nil, err
	}
	steps = append(steps, aliases...)
	keys, err := s.keySteps(ctx, want, sec)
	if err != nil {
		return nil, err
	}
	steps = append(steps, keys...)
	if prune {
		deletes, err := s.deleteSteps(ctx, want)
		if err != nil {
			return nil, err
		}
		steps = append(steps, deletes...)
	}
	return steps, nil
}

// secret resolves an "env:NAME" value: ok is false when NAME is not in sec.
func secret(sec Secrets, ref string) (name, value string, ok bool) {
	name, err := SecretRef(ref)
	if err != nil {
		return "", "", false
	}
	value, ok = sec[name]
	return name, value, ok
}

func unset(what, env string) error {
	return fmt.Errorf("%w: %s: secret env:%s is not set", ErrInvalid, what, env)
}

func created(kind, name string, run func(ctx context.Context) (string, error)) step {
	return step{Change{Op: "create", Kind: kind, Name: name, Fields: []string{}}, run}
}

func noValue(err error) (string, error) { return "", err }

func byName[T any](name func(T) string) func(a, b T) int {
	return func(a, b T) int { return cmp.Compare(name(a), name(b)) }
}

func (s *Service) providerSteps(ctx context.Context, want File, sec Secrets, prune bool) ([]step, error) {
	have, err := s.p.List(ctx)
	if err != nil {
		return nil, err
	}
	stored := map[string]Provider{}
	for _, p := range have {
		stored[p.Name] = p
	}
	var steps []step
	for _, wp := range slices.SortedFunc(slices.Values(want.Providers), byName(func(p FileProvider) string { return p.Name })) {
		p := Provider{Name: wp.Name, Kind: wp.Kind, Type: wp.Type, BaseURL: wp.BaseURL, AllowPrivateNetwork: wp.AllowPrivateNetwork}
		cur, exists := stored[wp.Name]
		if !exists {
			for _, k := range wp.Keys {
				env, v, ok := secret(sec, k.Value)
				if !ok {
					return nil, unset(fmt.Sprintf("provider %q key %q", wp.Name, k.Name), env)
				}
				p.Keys = append(p.Keys, ProviderKey{Name: k.Name, Value: v})
			}
			steps = append(steps, created("provider", wp.Name, func(ctx context.Context) (string, error) {
				_, err := s.p.Create(ctx, p)
				return "", err
			}))
			continue
		}

		var fields []string
		if cur.Kind != p.Kind {
			fields = append(fields, "kind")
		}
		if cur.Type != p.Type {
			fields = append(fields, "type")
		}
		if cur.BaseURL != p.BaseURL {
			fields = append(fields, "base_url")
		}
		if cur.AllowPrivateNetwork != p.AllowPrivateNetwork {
			fields = append(fields, "allow_private_network")
		}
		keysChanged, err := providerKeys(&p, cur, wp, sec, prune)
		if err != nil {
			return nil, err
		}
		if keysChanged {
			fields = append(fields, "keys")
		}
		if len(fields) == 0 {
			continue
		}
		steps = append(steps, step{Change{Op: "update", Kind: "provider", Name: wp.Name, Fields: fields}, func(ctx context.Context) (string, error) {
			_, err := s.p.Update(ctx, p)
			return "", err
		}})
	}
	return steps, nil
}

// providerKeys sets p.Keys for an update of cur to wp, the port's way: an empty Value keeps the stored secret, a
// key left out is deleted. Without prune, a stored key wp leaves out is kept. It reports whether the keys change.
func providerKeys(p *Provider, cur Provider, wp FileProvider, sec Secrets, prune bool) (bool, error) {
	stored := map[string]string{}
	for _, k := range cur.Keys {
		stored[k.Name] = k.Value
	}
	if len(wp.Keys) == 0 && len(cur.Keys) > 0 && prune {
		return false, fmt.Errorf("%w: provider %q: list at least one of its keys; a provider's keys cannot all be removed", ErrInvalid, wp.Name)
	}
	changed := false
	listed := map[string]bool{}
	for _, k := range wp.Keys {
		listed[k.Name] = true
		env, v, ok := secret(sec, k.Value)
		old, had := stored[k.Name]
		switch {
		case !had && !ok:
			return false, unset(fmt.Sprintf("provider %q key %q", wp.Name, k.Name), env)
		case !had || (ok && v != old):
			changed = true
		default:
			v = "" // keep the stored secret
		}
		p.Keys = append(p.Keys, ProviderKey{Name: k.Name, Value: v})
	}
	for _, k := range cur.Keys {
		if listed[k.Name] {
			continue
		}
		if prune {
			changed = true
		} else {
			p.Keys = append(p.Keys, ProviderKey{Name: k.Name})
		}
	}
	return changed, nil
}

func toAlias(fa FileAlias) alias.Alias {
	a := alias.Alias{
		Name: fa.Name, Kind: alias.Kind(fa.Kind), Members: make([]alias.Member, len(fa.Members)),
		Capabilities: slices.Clone(fa.Capabilities), ContextWindow: fa.ContextWindow, Strategy: alias.Strategy(fa.Strategy),
	}
	for i, m := range fa.Members {
		a.Members[i] = alias.Member{Ref: m}
	}
	return a
}

// membersFirst orders aliases so that each comes after the aliases among them it names, by name otherwise. A
// cycle is left for the port to reject.
func membersFirst(all []alias.Alias) []alias.Alias {
	named := map[string]alias.Alias{}
	for _, a := range all {
		named[a.Name] = a
	}
	var out []alias.Alias
	seen := map[string]bool{}
	var visit func(a alias.Alias)
	visit = func(a alias.Alias) {
		if seen[a.Name] {
			return
		}
		seen[a.Name] = true
		for _, m := range a.Members {
			if dep, ok := named[m.Ref]; ok {
				visit(dep)
			}
		}
		out = append(out, a)
	}
	for _, a := range slices.SortedFunc(slices.Values(all), byName(func(a alias.Alias) string { return a.Name })) {
		visit(a)
	}
	return out
}

func (s *Service) aliasSteps(ctx context.Context, want File) ([]step, error) {
	have, err := s.a.List(ctx)
	if err != nil {
		return nil, err
	}
	stored := map[string]alias.Alias{}
	for _, a := range have {
		stored[a.Name] = a
	}
	wanted := make([]alias.Alias, len(want.Aliases))
	for i, fa := range want.Aliases {
		wanted[i] = toAlias(fa)
	}
	var steps []step
	for _, a := range membersFirst(wanted) {
		cur, exists := stored[a.Name]
		if !exists {
			steps = append(steps, created("alias", a.Name, func(ctx context.Context) (string, error) {
				_, err := s.a.Put(ctx, a, true)
				return "", err
			}))
			continue
		}
		var fields []string
		if cur.Kind != a.Kind {
			fields = append(fields, "kind")
		}
		if !slices.Equal(cur.Members, a.Members) {
			fields = append(fields, "members")
		}
		if !slices.Equal(cur.Capabilities, a.Capabilities) {
			fields = append(fields, "capabilities")
		}
		if cur.ContextWindow != a.ContextWindow {
			fields = append(fields, "context_window")
		}
		if cur.Strategy != a.Strategy {
			fields = append(fields, "strategy")
		}
		if len(fields) == 0 {
			continue
		}
		steps = append(steps, step{Change{Op: "update", Kind: "alias", Name: a.Name, Fields: fields}, func(ctx context.Context) (string, error) {
			_, err := s.a.Put(ctx, a, false)
			return "", err
		}})
	}
	return steps, nil
}

func (s *Service) keySteps(ctx context.Context, want File, sec Secrets) ([]step, error) {
	have, err := s.k.List(ctx)
	if err != nil {
		return nil, err
	}
	stored := map[string]Key{}
	for _, k := range have {
		stored[k.Name] = k
	}
	var steps []step
	for _, wk := range slices.SortedFunc(slices.Values(want.Keys), byName(func(k FileKey) string { return k.Name })) {
		var (
			env, value string
			ok         bool
		)
		if wk.Value != "" {
			env, value, ok = secret(sec, wk.Value)
		}
		allow := slices.Clone(wk.Allow)
		if allow == nil {
			allow = []string{}
		}
		enabled := wk.Enabled == nil || *wk.Enabled
		name := wk.Name

		cur, exists := stored[name]
		if !exists {
			if wk.Value != "" && !ok {
				return nil, unset(fmt.Sprintf("key %q", name), env)
			}
			steps = append(steps, created("key", name, func(ctx context.Context) (string, error) {
				_, v, err := s.k.Create(ctx, name, allow, value, enabled)
				if err != nil {
					return "", err
				}
				if value != "" {
					return "", nil
				}
				return v, nil
			}))
			continue
		}

		var fields []string
		if !slices.Equal(cur.Allow, allow) {
			fields = append(fields, "allow")
		}
		if cur.Enabled != enabled {
			fields = append(fields, "enabled")
		}
		if ok {
			match, err := s.k.MatchesValue(ctx, cur.ID, value)
			if err != nil {
				return nil, err
			}
			if !match {
				fields = append(fields, "value")
			}
		}
		if len(fields) == 0 {
			continue
		}
		patch := KeyPatch{}
		if slices.Contains(fields, "allow") {
			patch.Allow = &allow
		}
		if slices.Contains(fields, "enabled") {
			patch.Enabled = &enabled
		}
		setValue := slices.Contains(fields, "value")
		id := cur.ID
		steps = append(steps, step{Change{Op: "update", Kind: "key", Name: name, Fields: fields}, func(ctx context.Context) (string, error) {
			if patch.Allow != nil || patch.Enabled != nil {
				if _, err := s.k.Update(ctx, id, patch); err != nil {
					return "", err
				}
			}
			if setValue {
				return noValue(s.k.SetValue(ctx, id, value))
			}
			return "", nil
		}})
	}
	return steps, nil
}

func deleted(kind, name string, run func(ctx context.Context) error) step {
	return step{Change{Op: "delete", Kind: kind, Name: name, Fields: []string{}}, func(ctx context.Context) (string, error) {
		return noValue(run(ctx))
	}}
}

// deleteSteps deletes what the store has and want does not: client keys, then aliases (an alias before the aliases
// it names), then providers.
func (s *Service) deleteSteps(ctx context.Context, want File) ([]step, error) {
	var steps []step

	keep := map[string]bool{}
	for _, k := range want.Keys {
		keep[k.Name] = true
	}
	keys, err := s.k.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, k := range slices.SortedFunc(slices.Values(keys), byName(func(k Key) string { return k.Name })) {
		if !keep[k.Name] {
			id := k.ID
			steps = append(steps, deleted("key", k.Name, func(ctx context.Context) error { return s.k.Delete(ctx, id) }))
		}
	}

	clear(keep)
	for _, a := range want.Aliases {
		keep[a.Name] = true
	}
	aliases, err := s.a.List(ctx)
	if err != nil {
		return nil, err
	}
	var gone []alias.Alias
	for _, a := range aliases {
		if !keep[a.Name] {
			gone = append(gone, a)
		}
	}
	gone = membersFirst(gone)
	slices.Reverse(gone)
	for _, a := range gone {
		name := a.Name
		steps = append(steps, deleted("alias", name, func(ctx context.Context) error { return s.a.Delete(ctx, name) }))
	}

	clear(keep)
	for _, p := range want.Providers {
		keep[p.Name] = true
	}
	providers, err := s.p.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, p := range slices.SortedFunc(slices.Values(providers), byName(func(p Provider) string { return p.Name })) {
		if !keep[p.Name] && slices.Contains(providerKinds, p.Kind) { // as Export, never a provider a document cannot hold
			name := p.Name
			steps = append(steps, deleted("provider", name, func(ctx context.Context) error { return s.p.Delete(ctx, name) }))
		}
	}
	return steps, nil
}
