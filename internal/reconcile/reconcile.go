// Package reconcile makes the front door (Bifrost) match remora's store: one provider per store provider, one key
// per provider key, one virtual key per client key and the routing rules of each alias. The store always wins.
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
	"sync"
	"sync/atomic"
	"time"

	"github.com/47vigen/remora/internal/alias"
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
// base belong to InternalRules: an entry whose Priority is in 1..RulePriorityBase-1 takes exactly that priority (each
// entry its own); while another engine rule still holds it, the entry takes a free one at or above the base and moves
// back on a later pass, once the holder is gone. The order between rules does not matter: an alias rule matches one
// exact model name, an alias's name never starts with a provider name, and the rules of one spreading alias exclude
// each other by the X-Remora-First value (DefaultRuleCEL), so a new member rule may sit after its default rule.
const RulePriorityBase = 1000

const defaultInterval = 60 * time.Second

// Reconciler pushes the store's state into the front door.
type Reconciler struct {
	Store         *store.Store
	Engine        Engine
	Internal      []InternalProvider    // pushed like store providers, never deleted, valid as alias member providers
	InternalRules []bifrost.RoutingRule // pushed by name like alias rules, never deleted (plan 03: one "remora-oauth-<name>" per OAuth provider); Priority as RulePriorityBase says
	Reserved      []string              // user-facing OAuth provider names: no store provider may take one; valid as alias member providers, mapped by ToEngineTarget
	// Filter prunes validated user-facing targets before compiling routing rules. It also reports providers skipped
	// for lack of a usable account. An empty result leaves the chain whole.
	Filter func(chain []alias.Target) (kept []alias.Target, unavailableProviders []string)
	// Also runs in every SyncAll (so in every Run pass) before the front-door pass: work that belongs to the same
	// reconcile cycle, such as the OAuth engine's own reconciliation. Errors are joined with the pass's.
	Also []func(ctx context.Context) error
	// Publish, when set, receives the live chains of every spreading alias after a pass commits; never inside Batch's
	// nested calls, never after a rollback.
	Publish    func(chains map[string]alias.Chain)
	Interval   time.Duration // 0 = 60s
	ForceEvery time.Duration // 0 = 1h between full syncs
	MaxDepth   int           // 0 = alias.DefaultMaxDepth
	// RestartCount, when set, reports the total engine restarts this process has seen.
	RestartCount func() int
	// SonioxURL is the speech adapter's base URL, e.g. "http://127.0.0.1:18090"; "" = a soniox provider is invalid.
	SonioxURL string
	// SonioxDeadline is the adapter's deadline; 0 = 30m. The front door waits 60 s longer.
	SonioxDeadline time.Duration
	Log            *slog.Logger

	syncMu       sync.Mutex // serializes engine passes, Change compensation and publication
	lastForce    time.Time
	lastRestarts int
	pending      map[string]bool            // engine mutations not yet acknowledged by a successful commit
	unavailable  map[unavailableMember]bool // skipped members from the last committed pass

	synced     atomic.Bool // a Sync has completed
	alsoSynced atomic.Bool // a SyncAll has run every Also step without an error
}

func (r *Reconciler) log() *slog.Logger {
	if r.Log == nil {
		return slog.New(slog.DiscardHandler)
	}
	return r.Log
}

// Sync reconciles from a store snapshot. An alias that does not compile is logged and its rule removed; any other
// invalid configuration fails the pass with ErrInvalid before the engine is called.
func (r *Reconciler) Sync(ctx context.Context) error {
	r.syncMu.Lock()
	defer r.syncMu.Unlock()
	return r.sync(ctx)
}

// Synced reports whether the front door holds the store's state: false until a Sync completes, and again after a
// pass that changed the engine fails, until a later pass succeeds. With Also steps, it also stays false until one
// SyncAll has run all of them without an error, so the engines behind the front door start from the store too.
func (r *Reconciler) Synced() bool {
	return r.synced.Load() && (len(r.Also) == 0 || r.alsoSynced.Load())
}

// sync runs with syncMu held. A failed full push leaves its trigger pending for the next sync.
func (r *Reconciler) sync(ctx context.Context) error {
	every := r.ForceEvery
	if every <= 0 {
		every = time.Hour
	}
	now := time.Now()
	var restarts int
	if r.RestartCount != nil {
		restarts = r.RestartCount()
	}
	forceAll := r.lastForce.IsZero() || restarts != r.lastRestarts || now.Sub(r.lastForce) >= every
	if err := r.runPasses(ctx, r.Store.Write, nil, false, forceAll); err != nil {
		return fmt.Errorf("reconcile: sync: %w", err)
	}
	if forceAll {
		r.lastForce, r.lastRestarts = now, restarts
	}
	r.synced.Store(true)
	return nil
}

var errStalePass = errors.New("reconcile: store changed during the pass")

// runPasses retries a stale snapshot twice. Unacknowledged mutations must be forced even when the old MAC matches.
func (r *Reconciler) runPasses(ctx context.Context, commit func(context.Context, func(*store.Tx) error) error, p *pass, strict, forceAll bool) (err error) {
	defer func() {
		if err != nil && len(r.pending) != 0 {
			r.synced.Store(false)
		}
	}()
	for range 3 {
		if p == nil {
			p = &pass{r: r}
			if err := r.Store.Read(ctx, func(tx *store.Tx) error { return p.read(ctx, tx, strict) }); err != nil {
				return err
			}
		}
		p.force, p.forceAll = r.pending, forceAll
		p.touched = map[string]bool{}
		err := p.push(ctx)
		if len(p.touched) != 0 {
			if r.pending == nil {
				r.pending = p.touched
			} else {
				maps.Copy(r.pending, p.touched)
			}
		}
		if err != nil {
			return err
		}
		err = commit(ctx, func(tx *store.Tx) error { return p.commit(ctx, tx) })
		if errors.Is(err, errStalePass) {
			p = nil
			continue
		}
		if err == nil {
			r.pending = nil
			for m := range p.desired.Unavailable {
				if !r.unavailable[m] {
					r.log().Info("alias member skipped: no enabled, signed-in account", "provider", m.Provider, "alias", m.Alias)
				}
			}
			r.unavailable = p.desired.Unavailable
			if r.Publish != nil {
				r.Publish(p.desired.Chains)
			}
		}
		return err
	}
	return errStalePass
}

// Change commits fn and validates its desired state before calling the engine. On failure it compensates the store
// write, then repushes the restored state if the engine was touched. Inside Batch, fn joins the batch transaction.
func (r *Reconciler) Change(ctx context.Context, fn func(ctx context.Context, tx *store.Tx) error) error {
	if tx, ok := store.TxFrom(ctx); ok {
		return fn(ctx, tx)
	}
	r.syncMu.Lock()
	defer r.syncMu.Unlock()

	p := &pass{r: r}
	change, err := r.Store.WriteWithUndo(ctx, func(tx *store.Tx) error {
		if err := fn(ctx, tx); err != nil {
			return err
		}
		return p.read(ctx, tx, true)
	})
	if err == nil {
		err = r.runPasses(ctx, change.Commit, p, true, false)
		if err == nil {
			return nil
		}
		recovery := context.WithoutCancel(ctx)
		if undoErr := change.Undo(recovery); undoErr != nil {
			change.Abandon(recovery)
			r.synced.Store(false)
			return fmt.Errorf("reconcile: change: %w", errors.Join(err, fmt.Errorf("compensate store change: %w", undoErr)))
		}
		if len(r.pending) != 0 {
			if recoveryErr := r.sync(recovery); recoveryErr != nil {
				r.log().Error("reconcile: recovery after a failed change", "err", recoveryErr)
			}
		}
	}
	return fmt.Errorf("reconcile: change: %w", err)
}

// Batch runs fn as one Change: every Change made with the context fn receives joins its store transaction and its
// one strict pass, so they are committed together or not at all.
func (r *Reconciler) Batch(ctx context.Context, fn func(ctx context.Context) error) error {
	return r.Change(ctx, func(ctx context.Context, tx *store.Tx) error { return fn(store.WithTx(ctx, tx)) })
}

// SyncAll runs every Also function and then one Sync pass. Every step runs; the errors are joined.
func (r *Reconciler) SyncAll(ctx context.Context) error {
	r.syncMu.Lock()
	defer r.syncMu.Unlock()

	var errs []error
	for _, fn := range r.Also {
		if err := fn(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) == 0 {
		r.alsoSynced.Store(true)
	}
	if err := r.sync(ctx); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// Run runs SyncAll now and then every Interval until ctx is done. Errors are logged.
func (r *Reconciler) Run(ctx context.Context) {
	interval := r.Interval
	if interval <= 0 {
		interval = defaultInterval
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if err := r.SyncAll(ctx); err != nil && ctx.Err() == nil {
			r.log().Error("reconcile: sync", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// pass carries the read snapshot and buffers engine output until the generation can be checked.
type pass struct {
	r          *Reconciler
	desired    *desiredState
	generation int64
	sign       func([]byte) ([]byte, error)
	forceAll   bool
	force      map[string]bool // objects to push even when their MAC matches
	touched    map[string]bool // objects this pass sent a mutating call for
	macs       map[string]string
	aliasIDs   map[string]string
	ruleIDs    map[string]string
	vks        []recordedVK
}

type recordedVK struct{ key, id, value string }

// stale is an engine object to delete after the pushes.
type stale struct{ provider, id, name string }

func (p *pass) read(ctx context.Context, tx *store.Tx, strict bool) (err error) {
	if p.generation, err = tx.Generation(ctx); err != nil {
		return err
	}
	if p.desired, err = p.r.desired(ctx, tx, strict); err != nil {
		return err
	}
	if p.macs, err = tx.EngineMACs(ctx); err != nil {
		return err
	}
	// MAC uses the transaction's sealer, not its database connection.
	p.sign = tx.MAC
	p.aliasIDs, err = tx.AliasRuleIDs(ctx)
	return err
}

func (p *pass) push(ctx context.Context) error {
	d := p.desired
	for _, name := range slices.Sorted(maps.Keys(d.Skipped)) {
		p.r.log().Error("reconcile: alias skipped", "alias", name, "err", d.Skipped[name])
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

	return nil
}

func (p *pass) commit(ctx context.Context, tx *store.Tx) error {
	generation, err := tx.Generation(ctx)
	if err != nil {
		return err
	}
	if generation != p.generation {
		return errStalePass
	}
	want := p.desired.objects()
	for obj, mac := range p.macs {
		switch {
		case !want[obj]:
			err = tx.DeleteEngineMAC(ctx, obj)
		case p.touched[obj]:
			err = tx.SetEngineMAC(ctx, obj, mac)
		}
		if err != nil {
			return err
		}
	}
	for _, vk := range p.vks {
		if err := tx.SetClientKeyVK(ctx, vk.key, vk.id, vk.value); err != nil {
			return err
		}
	}
	for _, name := range p.desired.Aliases {
		if p.ruleIDs[name] != p.aliasIDs[name] {
			if err := tx.SetAliasRuleID(ctx, name, p.ruleIDs[name]); err != nil {
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
	m, err := p.sign(b)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(m), nil
}

// changed reports whether obj must be pushed although it exists in the engine.
func (p *pass) changed(obj, mac string) bool {
	return p.forceAll || p.force[obj] || p.macs[obj] != mac
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
		p.macs[obj] = mac
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
			p.macs[obj] = mac
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
			p.macs[obj] = mac
		}
		// Re-record the engine's id and value when they differ from the store (a recreated key, or a key whose
		// recording was rolled back).
		value := cur.Value
		if value == "" {
			value = dv.StoredValue
		}
		if cur.ID != dv.StoredID || value != dv.StoredValue {
			p.vks = append(p.vks, recordedVK{dv.ClientKeyID, cur.ID, value})
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
	used := map[int]bool{}
	holder := map[int]string{} // priority → id of an engine rule holding it
	byName := map[string][]bifrost.RoutingRule{}
	for _, er := range eng {
		used[er.Priority] = true
		holder[er.Priority] = er.ID
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
	p.ruleIDs = map[string]string{}
	for _, dr := range d.Rules {
		name := dr.Rule.Name
		var cur *bifrost.RoutingRule
		for i, er := range byName[name] {
			if cur == nil || (dr.Alias != "" && er.ID == p.aliasIDs[dr.Alias]) {
				cur = &byName[name][i]
			}
		}
		rule := dr.Rule
		fixed := dr.Alias == "" && rule.Priority > 0 && rule.Priority < RulePriorityBase
		switch {
		case fixed && (holder[rule.Priority] == "" || (cur != nil && holder[rule.Priority] == cur.ID)):
			used[rule.Priority] = true
		case cur != nil && cur.Priority >= RulePriorityBase:
			rule.Priority = cur.Priority
		default:
			if fixed {
				p.r.log().Warn("reconcile: routing rule priority held by another rule; using a free one until it is gone",
					"rule", name, "priority", rule.Priority)
			}
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
			p.macs[dr.Object] = mac
		}
		kept[name] = rule.ID
		if dr.Alias != "" {
			p.ruleIDs[dr.Alias] = rule.ID
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
