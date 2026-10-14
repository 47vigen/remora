// Package reconcile makes the front door (Bifrost) match remora's store: one provider per store provider, one key
// per provider key, one virtual key per client key and one routing rule per alias. The store always wins.
package reconcile

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"sync/atomic"
	"time"

	"github.com/47vigen/remora/internal/engine/bifrost"
	"github.com/47vigen/remora/internal/store"
)

// Engine is the front door's management API. *bifrost.Client satisfies it.
type Engine interface {
	ListProviders(ctx context.Context) ([]bifrost.Provider, error)
	CreateProvider(ctx context.Context, p bifrost.Provider) error
	UpdateProvider(ctx context.Context, p bifrost.Provider) error
	DeleteProvider(ctx context.Context, name string) error
	ListKeys(ctx context.Context, provider string) ([]bifrost.Key, error)
	CreateKey(ctx context.Context, provider string, k bifrost.Key) error
	UpdateKey(ctx context.Context, provider string, k bifrost.Key) error
	DeleteKey(ctx context.Context, provider, id string) error
	ListVirtualKeys(ctx context.Context) ([]bifrost.VirtualKey, error)
	CreateVirtualKey(ctx context.Context, v bifrost.VirtualKey) (bifrost.VirtualKey, error)
	UpdateVirtualKey(ctx context.Context, v bifrost.VirtualKey) error
	DeleteVirtualKey(ctx context.Context, id string) error
	ListRoutingRules(ctx context.Context) ([]bifrost.RoutingRule, error)
	CreateRoutingRule(ctx context.Context, r bifrost.RoutingRule) (bifrost.RoutingRule, error)
	UpdateRoutingRule(ctx context.Context, r bifrost.RoutingRule) error
	DeleteRoutingRule(ctx context.Context, id string) error
}

var _ Engine = (*bifrost.Client)(nil)

// InternalProvider is a front-door provider remora runs itself (plan 03 appends the OAuth engine). Key ids must be
// unique across all providers; the engine also wants key names unique, so use the id as the name.
type InternalProvider struct {
	Provider bifrost.Provider
	Keys     []bifrost.Key
}

// ErrInvalid means the store holds a configuration the front door cannot serve. It wraps the cause.
var ErrInvalid = errors.New("reconcile: invalid configuration")

// RulePriorityBase is the lowest priority the reconciler gives a routing rule it pushes.
//
// The engine refuses two rules with one priority in a scope (409), so every pushed rule, alias or internal, gets
// its own. A rule keeps the priority it already has in the engine when that is at or above the base; a new rule,
// or one below the base, takes the lowest priority at or above the base that no rule in the engine holds. Adding,
// changing or removing one alias therefore never renumbers another, and no update can collide. Priorities below the
// base are left to rules remora does not push (plan 03's "remora-oauth-<name>" rules made before it fills
// InternalRules, or anything else); the Priority of an InternalRules entry is ignored. The order between rules does
// not matter: an alias rule matches one exact model name, and an alias's name never starts with a provider name.
const RulePriorityBase = 1000

const defaultInterval = 60 * time.Second

// Reconciler pushes the store's state into the front door.
type Reconciler struct {
	Store          *store.Store
	Engine         Engine
	Internal       []InternalProvider    // pushed like store providers, never deleted, valid as alias member providers
	InternalRules  []bifrost.RoutingRule // pushed by name like alias rules, never deleted (plan 03: one "remora-oauth-<name>" per OAuth provider)
	Reserved       []string              // user-facing OAuth provider names: no store provider may take one; valid as alias member providers
	Interval       time.Duration         // 0 = 60s
	MaxDepth       int                   // 0 = alias.DefaultMaxDepth
	SonioxURL      string                // the speech adapter's base URL, e.g. "http://127.0.0.1:18090"; "" = a soniox provider is invalid
	SonioxDeadline time.Duration         // the adapter's deadline; 0 = 30m. The front door waits 60 s longer
	Log            *slog.Logger

	synced atomic.Bool // a Sync has completed
}

func (r *Reconciler) log() *slog.Logger {
	if r.Log == nil {
		return slog.New(slog.DiscardHandler)
	}
	return r.Log
}

// Sync runs one pass inside store.Write. An alias that does not compile is logged and its rule removed; any other
// invalid configuration fails the pass with ErrInvalid before the engine is called.
func (r *Reconciler) Sync(ctx context.Context) error {
	if err := r.sync(ctx, nil); err != nil {
		return err
	}
	r.synced.Store(true)
	return nil
}

// Synced reports whether the front door holds the store's state: false until a Sync completes, and again after a
// pass that changed the engine fails, until a later pass succeeds.
func (r *Reconciler) Synced() bool { return r.synced.Load() }

// sync runs one non-strict pass; objects in force are pushed even when their MAC matches. A pass that fails after
// it changed the engine leaves it partly reconciled, so Synced reports false until a pass succeeds again.
func (r *Reconciler) sync(ctx context.Context, force map[string]bool) error {
	touched := map[string]bool{}
	err := r.Store.Write(ctx, func(tx *store.Tx) error {
		return (&pass{r: r, tx: tx, force: force, touched: touched}).run(ctx)
	})
	if err != nil {
		if len(touched) > 0 {
			r.synced.Store(false)
		}
		return fmt.Errorf("reconcile: sync: %w", err)
	}
	return nil
}

// Change runs fn and a strict pass in one store.Write, so a change the engine refuses is not committed. When the pass
// fails after it changed the engine, a recovery Sync pushes the committed state again, forcing every object the
// failed pass touched, because the rolled-back MAC still matches the committed state while the engine holds the
// newer write. The first error is returned.
func (r *Reconciler) Change(ctx context.Context, fn func(ctx context.Context, tx *store.Tx) error) error {
	touched := map[string]bool{}
	err := r.Store.Write(ctx, func(tx *store.Tx) error {
		if err := fn(ctx, tx); err != nil {
			return err
		}
		return (&pass{r: r, tx: tx, strict: true, touched: touched}).run(ctx)
	})
	if err == nil {
		return nil
	}
	if len(touched) > 0 {
		if rerr := r.sync(context.WithoutCancel(ctx), touched); rerr != nil {
			r.log().Error("reconcile: recovery after a failed change", "err", rerr)
		} else {
			r.synced.Store(true)
		}
	}
	return fmt.Errorf("reconcile: change: %w", err)
}

// Run syncs now and then every Interval until ctx is done. Errors are logged.
func (r *Reconciler) Run(ctx context.Context) {
	interval := r.Interval
	if interval <= 0 {
		interval = defaultInterval
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if err := r.Sync(ctx); err != nil && ctx.Err() == nil {
			r.log().Error("reconcile: sync", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// pass is one reconcile pass inside a store transaction.
type pass struct {
	r       *Reconciler
	tx      *store.Tx
	strict  bool
	force   map[string]bool // objects to push even when their MAC matches
	touched map[string]bool // objects this pass sent a mutating call for; survives a rollback
	macs    map[string]string
}

// stale is an engine object to delete after the pushes.
type stale struct{ provider, id, name string }

func (p *pass) run(ctx context.Context) error {
	d, err := p.r.desired(ctx, p.tx, p.strict)
	if err != nil {
		return err
	}
	for _, name := range slices.Sorted(maps.Keys(d.Skipped)) {
		p.r.log().Error("reconcile: alias skipped", "alias", name, "err", d.Skipped[name])
	}
	if p.macs, err = p.tx.EngineMACs(ctx); err != nil {
		return err
	}
	e := p.r.Engine
	engProviders, err := e.ListProviders(ctx)
	if err != nil {
		return fmt.Errorf("list providers: %w", err)
	}
	engVKs, err := e.ListVirtualKeys(ctx)
	if err != nil {
		return fmt.Errorf("list virtual keys: %w", err)
	}
	engRules, err := e.ListRoutingRules(ctx)
	if err != nil {
		return fmt.Errorf("list routing rules: %w", err)
	}

	// Create or update: providers, keys, virtual keys, rules. A provider must exist and have a key before a rule may
	// name it as a fallback.
	staleProviders, err := p.pushProviders(ctx, d, engProviders)
	if err != nil {
		return err
	}
	staleKeys, err := p.pushKeys(ctx, d, engProviders)
	if err != nil {
		return err
	}
	staleVKs, err := p.pushVKs(ctx, d, engVKs)
	if err != nil {
		return err
	}
	staleRules, err := p.pushRules(ctx, d, engRules)
	if err != nil {
		return err
	}

	// Delete in reverse order: rules, virtual keys, keys, providers.
	for _, s := range staleRules {
		if err := p.mutate("", func() error { return e.DeleteRoutingRule(ctx, s.id) }); err != nil {
			return fmt.Errorf("delete routing rule %q: %w", s.name, err)
		}
	}
	for _, s := range staleVKs {
		if err := p.mutate("", func() error { return e.DeleteVirtualKey(ctx, s.id) }); err != nil {
			return fmt.Errorf("delete virtual key %q: %w", s.name, err)
		}
	}
	for _, s := range staleKeys {
		if err := p.mutate("", func() error { return e.DeleteKey(ctx, s.provider, s.id) }); err != nil {
			return fmt.Errorf("delete key %q of %q: %w", s.id, s.provider, err)
		}
	}
	for _, s := range staleProviders {
		if err := p.mutate("", func() error { return e.DeleteProvider(ctx, s.name) }); err != nil {
			return fmt.Errorf("delete provider %q: %w", s.name, err)
		}
	}

	// Forget the MACs of objects that are no longer wanted.
	want := d.objects()
	for obj := range p.macs {
		if !want[obj] {
			if err := p.tx.DeleteEngineMAC(ctx, obj); err != nil {
				return err
			}
		}
	}
	return nil
}

// mutate marks obj touched, then calls fn. A delete of something already gone is not an error. A delete is
// recorded under "", which forces nothing but still makes a failed Change run its recovery Sync: a pass that only
// deleted must re-create what the rolled-back store still holds.
func (p *pass) mutate(obj string, fn func() error) error {
	p.touched[obj] = true
	err := fn()
	if errors.Is(err, bifrost.ErrNotFound) && obj == "" {
		return nil
	}
	return err
}

// mac returns the hex MAC of v's JSON (secrets included).
func (p *pass) mac(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("encode engine object: %w", err)
	}
	m, err := p.tx.MAC(b)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(m), nil
}

// changed reports whether obj must be pushed although it exists in the engine.
func (p *pass) changed(obj, mac string) bool {
	return p.force[obj] || p.macs[obj] != mac
}

func (p *pass) pushProviders(ctx context.Context, d *desiredState, eng []bifrost.Provider) ([]stale, error) {
	exists := map[string]bool{}
	for _, ep := range eng {
		exists[ep.Name] = true
	}
	wanted := map[string]bool{}
	for _, dp := range d.Providers {
		wanted[dp.Provider.Name] = true
		obj := providerObject(dp.Provider.Name)
		mac, err := p.mac(dp.Provider)
		if err != nil {
			return nil, err
		}
		switch {
		case !exists[dp.Provider.Name]:
			err = p.mutate(obj, func() error { return p.r.Engine.CreateProvider(ctx, dp.Provider) })
		case p.changed(obj, mac):
			err = p.mutate(obj, func() error { return p.r.Engine.UpdateProvider(ctx, dp.Provider) })
		default:
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("push provider %q: %w", dp.Provider.Name, err)
		}
		if err := p.tx.SetEngineMAC(ctx, obj, mac); err != nil {
			return nil, err
		}
	}
	var out []stale
	for _, ep := range eng {
		if !wanted[ep.Name] && ep.Name != bifrost.OAuthProvider {
			out = append(out, stale{name: ep.Name})
		}
	}
	return out, nil
}

// pushKeys pushes the keys of every wanted provider and returns the engine keys under them that are not wanted.
// Key ids are unique across the engine's providers, so a key that moved to another provider is deleted from the
// old one before it is created under the new one.
func (p *pass) pushKeys(ctx context.Context, d *desiredState, eng []bifrost.Provider) ([]stale, error) {
	names := map[string]bool{}
	for _, ep := range eng {
		names[ep.Name] = true
	}
	for _, dp := range d.Providers {
		names[dp.Provider.Name] = true
	}
	owner := map[string]string{}          // key id → provider holding it in the engine
	engKeys := map[string][]bifrost.Key{} // by provider
	for _, name := range slices.Sorted(maps.Keys(names)) {
		keys, err := p.r.Engine.ListKeys(ctx, name)
		if errors.Is(err, bifrost.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("list keys of %q: %w", name, err)
		}
		engKeys[name] = keys
		for _, k := range keys {
			owner[k.ID] = name
		}
	}

	var out []stale
	for _, dp := range d.Providers {
		prov := dp.Provider.Name
		wanted := map[string]bool{}
		for _, k := range dp.Keys {
			wanted[k.ID] = true
			obj := keyObject(k.ID)
			mac, err := p.mac(struct {
				Provider string
				Key      bifrost.Key
			}{prov, k})
			if err != nil {
				return nil, err
			}
			holder, held := owner[k.ID]
			if held && holder != prov {
				if err := p.mutate(obj, func() error { return p.r.Engine.DeleteKey(ctx, holder, k.ID) }); err != nil && !errors.Is(err, bifrost.ErrNotFound) {
					return nil, fmt.Errorf("move key %q from %q: %w", k.ID, holder, err)
				}
				held = false
			}
			switch {
			case !held:
				err = p.mutate(obj, func() error { return p.r.Engine.CreateKey(ctx, prov, k) })
			case p.changed(obj, mac):
				// The engine refuses an update without the value, so the secret is always sent.
				err = p.mutate(obj, func() error { return p.r.Engine.UpdateKey(ctx, prov, k) })
			default:
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("push key %q of %q: %w", k.ID, prov, err)
			}
			if err := p.tx.SetEngineMAC(ctx, obj, mac); err != nil {
				return nil, err
			}
		}
		for _, ek := range engKeys[prov] {
			if !wanted[ek.ID] {
				out = append(out, stale{provider: prov, id: ek.ID})
			}
		}
	}
	return out, nil
}

func (p *pass) pushVKs(ctx context.Context, d *desiredState, eng []bifrost.VirtualKey) ([]stale, error) {
	byName := map[string]bifrost.VirtualKey{}
	for _, v := range eng {
		byName[v.Name] = v
	}
	wanted := map[string]bool{}
	for _, dv := range d.VKs {
		wanted[dv.VK.Name] = true
		obj := vkObject(dv.ClientKeyID)
		mac, err := p.mac(dv.VK)
		if err != nil {
			return nil, err
		}
		cur, exists := byName[dv.VK.Name]
		switch {
		case !exists:
			var created bifrost.VirtualKey
			err = p.mutate(obj, func() (err error) {
				created, err = p.r.Engine.CreateVirtualKey(ctx, dv.VK)
				return err
			})
			cur = created
		case p.changed(obj, mac):
			v := dv.VK
			v.ID = cur.ID
			err = p.mutate(obj, func() error { return p.r.Engine.UpdateVirtualKey(ctx, v) })
		default:
			mac = "" // unchanged: keep the recorded MAC
		}
		if err != nil {
			return nil, fmt.Errorf("push virtual key %q: %w", dv.VK.Name, err)
		}
		if mac != "" {
			if err := p.tx.SetEngineMAC(ctx, obj, mac); err != nil {
				return nil, err
			}
		}
		// Re-record the engine's id and value when they differ from the store (a recreated key, or a key whose
		// recording was rolled back).
		value := cur.Value
		if value == "" {
			value = dv.StoredValue
		}
		if cur.ID != dv.StoredID || value != dv.StoredValue {
			if err := p.tx.SetClientKeyVK(ctx, dv.ClientKeyID, cur.ID, value); err != nil {
				return nil, err
			}
		}
	}
	var out []stale
	for _, v := range eng {
		if !wanted[v.Name] {
			out = append(out, stale{id: v.ID, name: v.Name})
		}
	}
	return out, nil
}

// pushRules pushes the wanted rules, each with its own priority (see RulePriorityBase), records alias rule ids, and
// returns the rules to delete: unknown names, and every rule but one for a wanted name. Rules of the OAuth engine
// integration are never returned.
func (p *pass) pushRules(ctx context.Context, d *desiredState, eng []bifrost.RoutingRule) ([]stale, error) {
	storedIDs, err := p.tx.AliasRuleIDs(ctx)
	if err != nil {
		return nil, err
	}
	used := map[int]bool{}
	byName := map[string][]bifrost.RoutingRule{}
	for _, er := range eng {
		used[er.Priority] = true
		byName[er.Name] = append(byName[er.Name], er)
	}
	nextFree := RulePriorityBase
	allocate := func() int {
		for used[nextFree] {
			nextFree++
		}
		used[nextFree] = true
		return nextFree
	}

	kept := map[string]string{} // rule name → id of the engine rule kept
	ruleIDs := map[string]string{}
	for _, dr := range d.Rules {
		name := dr.Rule.Name
		var cur *bifrost.RoutingRule
		for i, er := range byName[name] {
			if cur == nil || (dr.Alias != "" && er.ID == storedIDs[dr.Alias]) {
				cur = &byName[name][i]
			}
		}
		rule := dr.Rule
		if cur != nil && cur.Priority >= RulePriorityBase {
			rule.Priority = cur.Priority
		} else {
			rule.Priority = allocate()
		}
		mac, err := p.mac(rule)
		if err != nil {
			return nil, err
		}
		switch {
		case cur == nil:
			var created bifrost.RoutingRule
			err = p.mutate(dr.Object, func() (err error) {
				created, err = p.r.Engine.CreateRoutingRule(ctx, rule)
				return err
			})
			rule.ID = created.ID
		case p.changed(dr.Object, mac) || cur.Priority != rule.Priority:
			rule.ID = cur.ID
			err = p.mutate(dr.Object, func() error { return p.r.Engine.UpdateRoutingRule(ctx, rule) })
		default:
			rule.ID, mac = cur.ID, ""
		}
		if err != nil {
			return nil, fmt.Errorf("push routing rule %q: %w", name, err)
		}
		if mac != "" {
			if err := p.tx.SetEngineMAC(ctx, dr.Object, mac); err != nil {
				return nil, err
			}
		}
		kept[name] = rule.ID
		if dr.Alias != "" {
			ruleIDs[dr.Alias] = rule.ID
		}
	}
	for _, name := range d.Aliases {
		if ruleIDs[name] != storedIDs[name] {
			if err := p.tx.SetAliasRuleID(ctx, name, ruleIDs[name]); err != nil {
				return nil, err
			}
		}
	}

	var out []stale
	for _, er := range eng {
		if id, ok := kept[er.Name]; (ok && id == er.ID) || (!ok && isOAuthRule(er.Name)) {
			continue
		}
		out = append(out, stale{id: er.ID, name: er.Name})
	}
	return out, nil
}
