package reconcile

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/47vigen/remora/internal/alias"
	"github.com/47vigen/remora/internal/engine/bifrost"
	"github.com/47vigen/remora/internal/store"
)

// Names that link engine objects to remora's store rows.
const (
	rulePrefix = "remora:alias:"
	vkPrefix   = "remora:key:"
)

// maxFallbacks bounds a round-robin alias's fallback chain.
const maxFallbacks = 3

// RuleName is the routing rule name of an alias.
func RuleName(alias string) string { return rulePrefix + alias }

// VirtualKeyName is the virtual key name of a client key.
func VirtualKeyName(clientKeyID string) string { return vkPrefix + clientKeyID }

// RuleCEL matches a request for the alias on every surface remora exposes: a bare model name parses to provider
// "" on the native routes and Anthropic, "openai" on the Responses WebSocket and "typesafe" on System One.
// The engine sees alias.EngineModel(name), never the alias name itself.
func RuleCEL(name string) string {
	return "model == " + strconv.Quote(alias.EngineModel(name)) + ` && provider in ["", "openai", "typesafe"]`
}

// isOAuthRule reports a routing rule the OAuth engine integration owns (plan 03); it is never deleted.
func isOAuthRule(name string) bool {
	return name == bifrost.OAuthProvider || strings.HasPrefix(name, bifrost.OAuthProvider+"-")
}

// engine_sync object names.
func providerObject(name string) string     { return "provider:" + name }
func keyObject(id string) string            { return "key:" + id }
func vkObject(clientKeyID string) string    { return "vk:" + clientKeyID }
func ruleObject(alias string) string        { return "rule:" + alias }
func internalRuleObject(name string) string { return "irule:" + name }

type desiredProvider struct {
	Provider bifrost.Provider
	Keys     []bifrost.Key
}

type desiredVK struct {
	ClientKeyID           string
	VK                    bifrost.VirtualKey // Name and Active
	StoredID, StoredValue string
}

type desiredRule struct {
	Object string
	Alias  string              // "" for an internal rule
	Rule   bifrost.RoutingRule // no ID; Priority is assigned by the pass
}

// desiredState is what the engine must hold, built from one store transaction.
type desiredState struct {
	Providers []desiredProvider // store providers, then internal ones
	VKs       []desiredVK
	Rules     []desiredRule // internal rules, then alias rules by alias name
	Aliases   []string      // every alias in the store, sorted
	Skipped   map[string]error
}

// objects returns every engine_sync object name of the state.
func (d *desiredState) objects() map[string]bool {
	out := map[string]bool{}
	for _, p := range d.Providers {
		out[providerObject(p.Provider.Name)] = true
		for _, k := range p.Keys {
			out[keyObject(k.ID)] = true
		}
	}
	for _, v := range d.VKs {
		out[vkObject(v.ClientKeyID)] = true
	}
	for _, r := range d.Rules {
		out[r.Object] = true
	}
	return out
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// desired builds and validates the engine state for the store as tx sees it. Every provider-level problem is an
// ErrInvalid error. A problem with one alias is an ErrInvalid error when strict; otherwise the alias is left out
// and listed in Skipped, so one bad alias never blocks the rest.
func (r *Reconciler) desired(ctx context.Context, tx *store.Tx, strict bool) (*desiredState, error) {
	providers, err := tx.Providers(ctx)
	if err != nil {
		return nil, err
	}
	keys, err := tx.ProviderKeys(ctx)
	if err != nil {
		return nil, err
	}
	clientKeys, err := tx.ClientKeys(ctx)
	if err != nil {
		return nil, err
	}
	aliases, err := tx.Aliases(ctx)
	if err != nil {
		return nil, err
	}

	d := &desiredState{Skipped: map[string]error{}}
	reserved := map[string]bool{}
	for _, n := range r.Reserved {
		reserved[n] = true
	}
	internal := map[string]bool{}
	keyed := map[string]bool{} // providers with at least one key: the engine accepts only those as a fallback
	keyIDs := map[string]bool{}
	for _, ip := range r.Internal {
		internal[ip.Provider.Name] = true
		keyed[ip.Provider.Name] = len(ip.Keys) > 0
		for _, k := range ip.Keys {
			keyIDs[k.ID] = true
		}
	}

	byName := map[string]int{}
	for _, p := range providers {
		bp, err := r.engineProvider(p, reserved, internal)
		if err != nil {
			return nil, err
		}
		byName[p.Name] = len(d.Providers)
		d.Providers = append(d.Providers, desiredProvider{Provider: bp})
	}
	for _, k := range keys {
		if strings.HasPrefix(k.Secret, "env.") || strings.HasPrefix(k.Secret, "vault.") || k.Secret == "" {
			return nil, invalid("provider %q key %q: the value must not be empty or start with \"env.\" or \"vault.\"", k.Provider, k.Name)
		}
		if keyIDs[k.ID] {
			return nil, invalid("provider key id %q is taken by an internal provider", k.ID)
		}
		i := byName[k.Provider]
		// The key id is both the engine key's id and its name: the engine wants both unique across providers.
		d.Providers[i].Keys = append(d.Providers[i].Keys, bifrost.Key{ID: k.ID, Name: k.ID, Value: k.Secret, Enabled: k.Enabled})
		keyed[k.Provider] = true
	}
	for _, ip := range r.Internal {
		d.Providers = append(d.Providers, desiredProvider{Provider: ip.Provider, Keys: slices.Clone(ip.Keys)})
	}

	for _, ck := range clientKeys {
		d.VKs = append(d.VKs, desiredVK{
			ClientKeyID: ck.ID,
			VK:          bifrost.VirtualKey{Name: VirtualKeyName(ck.ID), Active: ck.Enabled},
			StoredID:    ck.VKID, StoredValue: ck.VKValue,
		})
	}

	for _, ir := range r.InternalRules {
		ir.ID = ""
		d.Rules = append(d.Rules, desiredRule{Object: internalRuleObject(ir.Name), Rule: ir})
	}

	isTarget := func(name string) bool {
		_, inStore := byName[name]
		return inStore || internal[name] || reserved[name]
	}
	// The engine accepts a fallback only on a provider it has initialised, which takes a key. A reserved provider is
	// left to plan 03, which maps it to the OAuth engine's provider.
	fallbackOK := func(name string) bool {
		if _, inStore := byName[name]; inStore || internal[name] {
			return keyed[name]
		}
		return true
	}
	for name := range aliases {
		d.Aliases = append(d.Aliases, name)
	}
	slices.Sort(d.Aliases)
	for _, name := range d.Aliases {
		rule, err := r.aliasRule(aliases, name, isTarget, fallbackOK, reserved)
		if err != nil {
			if strict {
				return nil, err
			}
			d.Skipped[name] = err
			continue
		}
		d.Rules = append(d.Rules, desiredRule{Object: ruleObject(name), Alias: name, Rule: rule})
	}
	return d, nil
}

// engineProvider validates a store provider and maps it to the front door's provider.
func (r *Reconciler) engineProvider(p store.Provider, reserved, internal map[string]bool) (bifrost.Provider, error) {
	switch {
	case p.Name == "" || strings.ContainsAny(p.Name, "/ "):
		return bifrost.Provider{}, invalid("provider name %q must be non-empty without \"/\" or spaces", p.Name)
	case p.Name == bifrost.OAuthProvider || strings.HasPrefix(p.Name, "remora-"):
		return bifrost.Provider{}, invalid("provider name %q is reserved for remora", p.Name)
	case reserved[p.Name] || internal[p.Name]:
		return bifrost.Provider{}, invalid("provider name %q is reserved for a subscription provider", p.Name)
	}
	bp := bifrost.Provider{Name: p.Name, BaseURL: engineBaseURL(p.BaseURL), AllowPrivateNetwork: p.AllowPrivateNetwork}
	switch p.Kind {
	case store.KindBuiltin:
		// The front door keys a standard provider by name, so 0.1 allows one provider per builtin type.
		if !bifrost.IsStandardProvider(p.Type) {
			return bifrost.Provider{}, invalid("provider %q: %q is not a builtin type", p.Name, p.Type)
		}
		if p.Name != p.Type {
			return bifrost.Provider{}, invalid("builtin provider %q must be named like its type %q", p.Name, p.Type)
		}
	case store.KindOpenAICompatible, store.KindTypeSafeCompatible:
		if p.Type != "" {
			return bifrost.Provider{}, invalid("provider %q of kind %s takes no type", p.Name, p.Kind)
		}
		if bifrost.IsStandardProvider(p.Name) {
			return bifrost.Provider{}, invalid("provider %q of kind %s must not take a builtin provider's name", p.Name, p.Kind)
		}
		if p.BaseURL == "" {
			return bifrost.Provider{}, invalid("provider %q of kind %s needs a base URL", p.Name, p.Kind)
		}
		bp.BaseType = "openai"
		if p.Kind == store.KindTypeSafeCompatible {
			bp.BaseType = "typesafe"
		}
	case store.KindSoniox:
		return r.engineSonioxProvider(p)
	default:
		return bifrost.Provider{}, invalid("provider %q: kind %q is not supported", p.Name, p.Kind)
	}
	return bp, nil
}

// engineBaseURL maps a provider's base URL to the front door's: the front door appends /v1/<endpoint> itself, while
// providers publish their base URL with the /v1 the OpenAI SDKs expect, so a trailing /v1 is dropped.
func engineBaseURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return strings.TrimRight(raw, "/")
	}
	u.Path = strings.TrimSuffix(strings.TrimRight(u.Path, "/"), "/v1")
	u.RawPath = ""
	return strings.TrimRight(u.String(), "/")
}

// aliasRule validates one alias and builds its routing rule: the compiled chain is validated whole, Filter drops
// cooling members, and members on a reserved (subscription) provider are mapped by ToEngineTarget. The first target is the rule's target, the rest
// are its fallbacks. A round-robin alias spreads the first try over the whole chain and falls back through all of it,
// since the engine keeps one fallback chain whichever target it picked.
func (r *Reconciler) aliasRule(all map[string]alias.Alias, name string, isTarget, fallbackOK func(string) bool, reserved map[string]bool) (bifrost.RoutingRule, error) {
	if err := alias.ValidName(name); err != nil {
		return bifrost.RoutingRule{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	targets, err := alias.Compile(all, name, r.MaxDepth)
	if err != nil {
		return bifrost.RoutingRule{}, fmt.Errorf("%w: alias %q: %w", ErrInvalid, name, err)
	}
	for _, t := range targets {
		if !isTarget(t.Provider) {
			return bifrost.RoutingRule{}, invalid("alias %q: member %s/%s: no provider %q", name, t.Provider, t.Model, t.Provider)
		}
		// The engine refuses a rule whose target or fallback names a provider without a key.
		if !fallbackOK(t.Provider) {
			return bifrost.RoutingRule{}, invalid("alias %q: member %s/%s: provider %q has no key", name, t.Provider, t.Model, t.Provider)
		}
	}
	if r.Filter != nil && len(targets) > 0 {
		if kept := r.Filter(slices.Clone(targets)); len(kept) > 0 {
			targets = kept
		}
	}
	rule := bifrost.RoutingRule{Name: RuleName(name), CEL: RuleCEL(name)}
	routes := make([]bifrost.Route, len(targets))
	for i, t := range targets {
		if reserved[t.Provider] {
			t = ToEngineTarget(t)
		}
		routes[i] = bifrost.Route(t)
	}
	rule.Target = routes[0]
	switch {
	case len(routes) == 1:
	case all[name].Strategy == alias.StrategyRoundRobin:
		// The front door cannot tell which spread member it picked, so the chain may repeat it; a cap keeps a failing
		// request from walking every member, each a full round through the OAuth engine's accounts.
		rule.Spread, rule.Fallbacks = routes[1:], routes[:min(len(routes), maxFallbacks)]
	default:
		rule.Fallbacks = routes[1:]
	}
	return rule, nil
}
