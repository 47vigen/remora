// Package reconciletest holds an in-memory front door for tests of code that drives a reconcile.Reconciler.
package reconciletest

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/47vigen/remora/internal/engine/bifrost"
)

// Call is one mutating call the fake engine received.
type Call struct {
	Op   string // method name, e.g. "UpdateRoutingRule"
	Name string // provider name, key id, virtual key name or rule name
	Arg  any    // the object sent (a copy)
}

// Engine is an in-memory front door. It models the engine's behaviour as observed live at transports/v2.3.0:
// a routing rule priority is unique in the global scope (409), rule names are not unique, key ids and key names
// are unique across providers (409), a key update needs a value (400), a rule fallback must name a provider that
// has a key (400; targets are not checked), deleting an unknown provider answers 200, updating an unknown provider
// creates it, and listing keys never returns their values.
type Engine struct {
	mu        sync.Mutex
	providers map[string]bifrost.Provider
	keys      map[string][]bifrost.Key // by provider, in insertion order
	vks       []bifrost.VirtualKey
	rules     []bifrost.RoutingRule
	seq       int
	calls     []Call
	fail      map[string]error // op → error returned once
	lists     int
}

// New returns an empty engine.
func New() *Engine {
	f := &Engine{}
	f.Reset()
	return f
}

// Reset wipes the engine, as a fresh data directory would.
func (f *Engine) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.providers = map[string]bifrost.Provider{}
	f.keys = map[string][]bifrost.Key{}
	f.vks = nil
	f.rules = nil
	f.calls = nil
	f.fail = map[string]error{}
}

// FailOnce makes the next call of op fail with a 500.
func (f *Engine) FailOnce(op string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail[op] = &bifrost.APIError{Status: http.StatusInternalServerError, Message: "injected failure"}
}

// Mutations returns the mutating calls received so far and forgets them.
func (f *Engine) Mutations() []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.calls
	f.calls = nil
	return c
}

// Lists returns how many times ListProviders was called, which counts reconcile passes that reached the engine.
func (f *Engine) Lists() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lists
}

func (f *Engine) nextID(prefix string) string {
	f.seq++
	return prefix + "-" + strconv.Itoa(f.seq)
}

// record logs a mutating call and returns the injected failure for op, if any.
func (f *Engine) record(op, name string, arg any) error {
	f.calls = append(f.calls, Call{Op: op, Name: name, Arg: arg})
	if err, ok := f.fail[op]; ok {
		delete(f.fail, op)
		return err
	}
	return nil
}

func apiErr(status int, format string, args ...any) error {
	return &bifrost.APIError{Status: status, Message: fmt.Sprintf(format, args...)}
}

// ListProviders lists the providers by name.
func (f *Engine) ListProviders(context.Context) ([]bifrost.Provider, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lists++
	out := make([]bifrost.Provider, 0, len(f.providers))
	for _, p := range f.providers {
		out = append(out, p)
	}
	slices.SortFunc(out, func(a, b bifrost.Provider) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

func (f *Engine) checkProvider(p bifrost.Provider) error {
	if p.BaseType != "" && bifrost.IsStandardProvider(p.Name) {
		return apiErr(http.StatusBadRequest, "custom provider %s must not be a standard provider", p.Name)
	}
	if p.BaseType == "" && !bifrost.IsStandardProvider(p.Name) {
		return apiErr(http.StatusBadRequest, "unknown provider %s needs custom_provider_config", p.Name)
	}
	return nil
}

// CreateProvider adds a provider; a duplicate answers 409.
func (f *Engine) CreateProvider(_ context.Context, p bifrost.Provider) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("CreateProvider", p.Name, p); err != nil {
		return err
	}
	if _, ok := f.providers[p.Name]; ok {
		return apiErr(http.StatusConflict, "Provider %s already exists", p.Name)
	}
	if err := f.checkProvider(p); err != nil {
		return err
	}
	f.providers[p.Name] = p
	return nil
}

// UpdateProvider creates an unknown provider, as the engine does.
func (f *Engine) UpdateProvider(_ context.Context, p bifrost.Provider) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("UpdateProvider", p.Name, p); err != nil {
		return err
	}
	if err := f.checkProvider(p); err != nil {
		return err
	}
	f.providers[p.Name] = p
	return nil
}

// DeleteProvider answers 200 for an unknown provider, as the engine does, and drops the provider's keys.
func (f *Engine) DeleteProvider(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("DeleteProvider", name, nil); err != nil {
		return err
	}
	delete(f.providers, name)
	delete(f.keys, name)
	return nil
}

// ListKeys lists a provider's keys with their values redacted.
func (f *Engine) ListKeys(_ context.Context, provider string) ([]bifrost.Key, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.providers[provider]; !ok {
		return nil, apiErr(http.StatusNotFound, "provider %s not found", provider)
	}
	out := make([]bifrost.Key, 0, len(f.keys[provider]))
	for _, k := range f.keys[provider] {
		k.Value = "" // the engine redacts values on list
		out = append(out, k)
	}
	return out, nil
}

// keyClash reports a key with the same id or name under any provider, other than (skipProvider, skipID).
func (f *Engine) keyClash(k bifrost.Key, skipProvider, skipID string) error {
	for p, keys := range f.keys {
		for _, o := range keys {
			if p == skipProvider && o.ID == skipID {
				continue
			}
			if o.ID == k.ID || o.Name == k.Name {
				return apiErr(http.StatusConflict, "API key names must be unique across providers")
			}
		}
	}
	return nil
}

// CreateKey adds a key; ids and names are unique across providers.
func (f *Engine) CreateKey(_ context.Context, provider string, k bifrost.Key) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("CreateKey", k.ID, k); err != nil {
		return err
	}
	if _, ok := f.providers[provider]; !ok {
		return apiErr(http.StatusNotFound, "provider %s not found", provider)
	}
	if k.Value == "" {
		return apiErr(http.StatusBadRequest, "Key value must not be empty")
	}
	if k.ID == "" {
		k.ID = f.nextID("key")
	}
	if err := f.keyClash(k, "", ""); err != nil {
		return err
	}
	f.keys[provider] = append(f.keys[provider], k)
	return nil
}

// UpdateKey replaces a key; the value must not be empty.
func (f *Engine) UpdateKey(_ context.Context, provider string, k bifrost.Key) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("UpdateKey", k.ID, k); err != nil {
		return err
	}
	if k.Value == "" {
		return apiErr(http.StatusBadRequest, "Key value must not be empty")
	}
	i := slices.IndexFunc(f.keys[provider], func(o bifrost.Key) bool { return o.ID == k.ID })
	if i < 0 {
		return apiErr(http.StatusNotFound, "key %s not found", k.ID)
	}
	if err := f.keyClash(k, provider, k.ID); err != nil {
		return err
	}
	f.keys[provider][i] = k
	return nil
}

// DeleteKey removes a key; an unknown key answers 404.
func (f *Engine) DeleteKey(_ context.Context, provider, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("DeleteKey", id, provider); err != nil {
		return err
	}
	i := slices.IndexFunc(f.keys[provider], func(o bifrost.Key) bool { return o.ID == id })
	if i < 0 {
		return apiErr(http.StatusNotFound, "key %s not found", id)
	}
	f.keys[provider] = slices.Delete(f.keys[provider], i, i+1)
	return nil
}

// ListVirtualKeys lists every virtual key, values included.
func (f *Engine) ListVirtualKeys(context.Context) ([]bifrost.VirtualKey, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.vks), nil
}

// CreateVirtualKey adds a virtual key with a fresh id and an engine-style value.
func (f *Engine) CreateVirtualKey(_ context.Context, v bifrost.VirtualKey) (bifrost.VirtualKey, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("CreateVirtualKey", v.Name, v); err != nil {
		return bifrost.VirtualKey{}, err
	}
	if slices.ContainsFunc(f.vks, func(o bifrost.VirtualKey) bool { return o.Name == v.Name }) {
		return bifrost.VirtualKey{}, apiErr(http.StatusConflict, "virtual key %s already exists", v.Name)
	}
	v.ID = f.nextID("vk")
	v.Value = "sk-bf-" + v.ID
	f.vks = append(f.vks, v)
	return v, nil
}

// UpdateVirtualKey changes a virtual key's name and state.
func (f *Engine) UpdateVirtualKey(_ context.Context, v bifrost.VirtualKey) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("UpdateVirtualKey", v.Name, v); err != nil {
		return err
	}
	i := slices.IndexFunc(f.vks, func(o bifrost.VirtualKey) bool { return o.ID == v.ID })
	if i < 0 {
		return apiErr(http.StatusNotFound, "virtual key %s not found", v.ID)
	}
	f.vks[i].Name, f.vks[i].Active = v.Name, v.Active
	return nil
}

// DeleteVirtualKey removes a virtual key; an unknown one answers 404.
func (f *Engine) DeleteVirtualKey(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	i := slices.IndexFunc(f.vks, func(o bifrost.VirtualKey) bool { return o.ID == id })
	name := ""
	if i >= 0 {
		name = f.vks[i].Name
	}
	if err := f.record("DeleteVirtualKey", name, id); err != nil {
		return err
	}
	if i < 0 {
		return apiErr(http.StatusNotFound, "virtual key %s not found", id)
	}
	f.vks = slices.Delete(f.vks, i, i+1)
	return nil
}

// ListRoutingRules lists every rule.
func (f *Engine) ListRoutingRules(context.Context) ([]bifrost.RoutingRule, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]bifrost.RoutingRule, 0, len(f.rules))
	for _, r := range f.rules {
		r.Fallbacks = slices.Clone(r.Fallbacks)
		out = append(out, r)
	}
	return out, nil
}

// checkRule enforces the unique priority and that every fallback provider is known, which takes a key.
func (f *Engine) checkRule(r bifrost.RoutingRule) error {
	for _, o := range f.rules {
		if o.ID != r.ID && o.Priority == r.Priority {
			return apiErr(http.StatusConflict, "routing rule with priority %d already exists for scope 'global'", r.Priority)
		}
	}
	for i, fb := range r.Fallbacks {
		if _, ok := f.providers[fb.Provider]; !ok || len(f.keys[fb.Provider]) == 0 {
			return apiErr(http.StatusBadRequest, "fallbacks[%d] %s/%s must use a known provider prefix", i, fb.Provider, fb.Model)
		}
	}
	return nil
}

// CreateRoutingRule adds a rule with a fresh id.
func (f *Engine) CreateRoutingRule(_ context.Context, r bifrost.RoutingRule) (bifrost.RoutingRule, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("CreateRoutingRule", r.Name, r); err != nil {
		return bifrost.RoutingRule{}, err
	}
	r.ID = ""
	if err := f.checkRule(r); err != nil {
		return bifrost.RoutingRule{}, err
	}
	r.ID = f.nextID("rule")
	r.Fallbacks = slices.Clone(r.Fallbacks)
	f.rules = append(f.rules, r)
	return r, nil
}

// UpdateRoutingRule replaces a rule by id.
func (f *Engine) UpdateRoutingRule(_ context.Context, r bifrost.RoutingRule) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("UpdateRoutingRule", r.Name, r); err != nil {
		return err
	}
	i := slices.IndexFunc(f.rules, func(o bifrost.RoutingRule) bool { return o.ID == r.ID })
	if i < 0 {
		return apiErr(http.StatusNotFound, "routing rule %s not found", r.ID)
	}
	if err := f.checkRule(r); err != nil {
		return err
	}
	r.Fallbacks = slices.Clone(r.Fallbacks)
	f.rules[i] = r
	return nil
}

// DeleteRoutingRule removes a rule; an unknown rule answers 404.
func (f *Engine) DeleteRoutingRule(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	i := slices.IndexFunc(f.rules, func(o bifrost.RoutingRule) bool { return o.ID == id })
	name := ""
	if i >= 0 {
		name = f.rules[i].Name
	}
	if err := f.record("DeleteRoutingRule", name, id); err != nil {
		return err
	}
	if i < 0 {
		return apiErr(http.StatusNotFound, "routing rule %s not found", id)
	}
	f.rules = slices.Delete(f.rules, i, i+1)
	return nil
}

// Seeding and inspection helpers. They bypass the Call log.

// SeedProvider adds a provider and keys without logging a call.
func (f *Engine) SeedProvider(p bifrost.Provider, keys ...bifrost.Key) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.providers[p.Name] = p
	f.keys[p.Name] = append(f.keys[p.Name], keys...)
}

// SeedVirtualKey adds an active virtual key without logging a call.
func (f *Engine) SeedVirtualKey(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.nextID("vk")
	f.vks = append(f.vks, bifrost.VirtualKey{ID: id, Name: name, Value: "sk-bf-" + id, Active: true})
}

// SeedRule adds a rule without logging a call and returns its id.
func (f *Engine) SeedRule(r bifrost.RoutingRule) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	r.ID = f.nextID("rule")
	f.rules = append(f.rules, r)
	return r.ID
}

// Provider returns the named provider.
func (f *Engine) Provider(name string) (bifrost.Provider, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.providers[name]
	return p, ok
}

// Key returns the key id under provider, value included.
func (f *Engine) Key(provider, id string) (bifrost.Key, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	i := slices.IndexFunc(f.keys[provider], func(o bifrost.Key) bool { return o.ID == id })
	if i < 0 {
		return bifrost.Key{}, false
	}
	return f.keys[provider][i], true
}

// VK returns the virtual key with that name.
func (f *Engine) VK(name string) (bifrost.VirtualKey, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	i := slices.IndexFunc(f.vks, func(o bifrost.VirtualKey) bool { return o.Name == name })
	if i < 0 {
		return bifrost.VirtualKey{}, false
	}
	return f.vks[i], true
}

// RulesNamed returns every rule with that name.
func (f *Engine) RulesNamed(name string) []bifrost.RoutingRule {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []bifrost.RoutingRule
	for _, r := range f.rules {
		if r.Name == name {
			out = append(out, r)
		}
	}
	return out
}
