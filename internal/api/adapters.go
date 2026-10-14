package api

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/47vigen/remora/internal/alias"
	"github.com/47vigen/remora/internal/reconcile"
	"github.com/47vigen/remora/internal/store"
	"github.com/47vigen/remora/internal/version"
)

// Adapters map the ports onto plans 01–02's store, reconciler and supervisor. Every write goes through
// reconcile.Reconciler.Change, so a change the front door refuses is not committed. Not wired yet, because their
// services land in plan 03: Accounts (accounts.Service), the cooling snapshot (cooling.Cooler) and the OAuth
// engine's provider names in EngineNames.Target and EngineFilter.

// NewSystem reports health and versions from the supervisor.
func NewSystem(src StatusSource) System { return systemAdapter{src: src} }

type systemAdapter struct{ src StatusSource }

func (a systemAdapter) Health(context.Context) (bool, map[string]error) {
	ok, out := true, map[string]error{}
	for _, e := range a.src.Status() {
		var err error
		if !e.Up {
			ok, err = false, errors.New("down")
		}
		out[componentName(e.Name)] = err
	}
	return ok, out
}

func (a systemAdapter) Version() (string, []EngineVersion) {
	status := a.src.Status()
	out := make([]EngineVersion, 0, len(status))
	for _, e := range status {
		out = append(out, EngineVersion{Name: e.Name, Version: e.Version})
	}
	return version.Version, out
}

// read runs fn in a store transaction that only reads. The store offers its repositories on write transactions.
func read(ctx context.Context, st *store.Store, fn func(tx *store.Tx) error) error {
	return st.Write(ctx, fn)
}

// NewProviders manages providers in st through rec.
func NewProviders(st *store.Store, rec *reconcile.Reconciler) Providers {
	return providersAdapter{st: st, rec: rec}
}

type providersAdapter struct {
	st  *store.Store
	rec *reconcile.Reconciler
}

func loadProviders(ctx context.Context, tx *store.Tx) ([]Provider, map[string][]store.ProviderKey, error) {
	ps, err := tx.Providers(ctx)
	if err != nil {
		return nil, nil, err
	}
	keys, err := tx.ProviderKeys(ctx)
	if err != nil {
		return nil, nil, err
	}
	byProvider := map[string][]store.ProviderKey{}
	for _, k := range keys {
		byProvider[k.Provider] = append(byProvider[k.Provider], k)
	}
	out := make([]Provider, 0, len(ps))
	for _, p := range ps {
		v := Provider{Name: p.Name, Kind: string(p.Kind), Type: p.Type, BaseURL: p.BaseURL, AllowPrivateNetwork: p.AllowPrivateNetwork}
		for _, k := range byProvider[p.Name] {
			v.Keys = append(v.Keys, ProviderKey{Name: k.Name, Value: k.Secret})
		}
		out = append(out, v)
	}
	return out, byProvider, nil
}

func (a providersAdapter) List(ctx context.Context) ([]Provider, error) {
	var out []Provider
	err := read(ctx, a.st, func(tx *store.Tx) error {
		var err error
		out, _, err = loadProviders(ctx, tx)
		return err
	})
	return out, err
}

func (a providersAdapter) Get(ctx context.Context, name string) (Provider, error) {
	all, err := a.List(ctx)
	if err != nil {
		return Provider{}, err
	}
	for _, p := range all {
		if p.Name == name {
			return p, nil
		}
	}
	return Provider{}, fmt.Errorf("%w: provider %s", store.ErrNotFound, name)
}

func (a providersAdapter) Create(ctx context.Context, p Provider) (Provider, error) {
	return a.put(ctx, p, true)
}

func (a providersAdapter) Update(ctx context.Context, p Provider) (Provider, error) {
	return a.put(ctx, p, false)
}

// put creates (create) or replaces a provider. On update, empty Keys keep the stored keys; otherwise the keys are
// replaced by name, a key with an empty Value keeping its stored secret.
func (a providersAdapter) put(ctx context.Context, p Provider, create bool) (Provider, error) {
	err := a.rec.Change(ctx, func(ctx context.Context, tx *store.Tx) error {
		existing, keys, err := loadProviders(ctx, tx)
		if err != nil {
			return err
		}
		exists := slices.ContainsFunc(existing, func(e Provider) bool { return e.Name == p.Name })
		switch {
		case create && exists:
			return fmt.Errorf("%w: provider %s", store.ErrDuplicate, p.Name)
		case !create && !exists:
			return fmt.Errorf("%w: provider %s", store.ErrNotFound, p.Name)
		}
		if err := tx.PutProvider(ctx, store.Provider{
			Name: p.Name, Kind: store.ProviderKind(p.Kind), Type: p.Type, BaseURL: p.BaseURL,
			AllowPrivateNetwork: p.AllowPrivateNetwork,
		}); err != nil {
			return err
		}
		if !create && len(p.Keys) == 0 {
			return nil
		}
		stored := map[string]store.ProviderKey{}
		for _, k := range keys[p.Name] {
			stored[k.Name] = k
		}
		wanted := map[string]bool{}
		for _, k := range p.Keys {
			wanted[k.Name] = true
			sk, had := stored[k.Name]
			if k.Value == "" && !had {
				return badRequest(fmt.Sprintf("key %q needs a value", k.Name))
			}
			sk.Provider, sk.Name, sk.Enabled = p.Name, k.Name, true
			if k.Value != "" {
				sk.Secret = k.Value
			}
			if err := tx.PutProviderKey(ctx, sk); err != nil {
				return err
			}
		}
		for name, sk := range stored {
			if !wanted[name] {
				if err := tx.DeleteProviderKey(ctx, sk.ID); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return Provider{}, err
	}
	return a.Get(ctx, p.Name)
}

func (a providersAdapter) Delete(ctx context.Context, name string) error {
	return a.rec.Change(ctx, func(ctx context.Context, tx *store.Tx) error { return tx.DeleteProvider(ctx, name) })
}

// Test has no backing service in plans 01–02.
func (a providersAdapter) Test(context.Context, string) (ProviderTest, error) {
	return ProviderTest{}, errNotImplemented
}

// NewAliases manages aliases in st through rec.
func NewAliases(st *store.Store, rec *reconcile.Reconciler) Aliases {
	return aliasesAdapter{st: st, rec: rec}
}

type aliasesAdapter struct {
	st  *store.Store
	rec *reconcile.Reconciler
}

func (a aliasesAdapter) maxDepth() int { return a.rec.MaxDepth } // 0 = alias.DefaultMaxDepth

func (a aliasesAdapter) List(ctx context.Context) ([]alias.Alias, error) {
	all, err := a.st.Aliases(ctx)
	if err != nil {
		return nil, err
	}
	return slices.SortedFunc(maps.Values(all), func(x, y alias.Alias) int { return cmp.Compare(x.Name, y.Name) }), nil
}

func (a aliasesAdapter) Get(ctx context.Context, name string) (alias.Alias, error) {
	all, err := a.st.Aliases(ctx)
	if err != nil {
		return alias.Alias{}, err
	}
	al, ok := all[name]
	if !ok {
		return alias.Alias{}, fmt.Errorf("%w: alias %s", store.ErrNotFound, name)
	}
	return al, nil
}

func (a aliasesAdapter) Put(ctx context.Context, al alias.Alias, create bool) (alias.Alias, error) {
	if err := alias.ValidName(al.Name); err != nil {
		return alias.Alias{}, err
	}
	err := a.rec.Change(ctx, func(ctx context.Context, tx *store.Tx) error {
		all, err := tx.Aliases(ctx)
		if err != nil {
			return err
		}
		_, exists := all[al.Name]
		switch {
		case create && exists:
			return fmt.Errorf("%w: alias %s", store.ErrDuplicate, al.Name)
		case !create && !exists:
			return fmt.Errorf("%w: alias %s", store.ErrNotFound, al.Name)
		}
		all[al.Name] = al
		if _, err := alias.Compile(all, al.Name, a.maxDepth()); err != nil {
			return err
		}
		return tx.PutAlias(ctx, al)
	})
	if err != nil {
		return alias.Alias{}, err
	}
	return a.Get(ctx, al.Name)
}

func (a aliasesAdapter) Delete(ctx context.Context, name string) error {
	return a.rec.Change(ctx, func(ctx context.Context, tx *store.Tx) error { return tx.DeleteAlias(ctx, name) })
}

func (a aliasesAdapter) Chain(ctx context.Context, name string) ([]alias.Target, error) {
	all, err := a.st.Aliases(ctx)
	if err != nil {
		return nil, err
	}
	if _, ok := all[name]; !ok {
		return nil, fmt.Errorf("%w: alias %s", store.ErrNotFound, name)
	}
	return alias.Compile(all, name, a.maxDepth())
}

func (a aliasesAdapter) Compile(ctx context.Context, al alias.Alias) ([]alias.Target, error) {
	if err := alias.ValidName(al.Name); err != nil {
		return nil, err
	}
	all, err := a.st.Aliases(ctx)
	if err != nil {
		return nil, err
	}
	if all == nil {
		all = map[string]alias.Alias{}
	}
	all[al.Name] = al
	return alias.Compile(all, al.Name, a.maxDepth())
}

// Cooling reports no cooling targets until plan 03's cooler is wired.
func (a aliasesAdapter) Cooling(context.Context) ([]Cooling, error) { return nil, nil }

// NewKeys manages client keys in st through rec.
func NewKeys(st *store.Store, rec *reconcile.Reconciler) Keys { return keysAdapter{st: st, rec: rec} }

type keysAdapter struct {
	st  *store.Store
	rec *reconcile.Reconciler
}

// toKey maps a stored client key. The store keeps neither a display prefix nor (in its struct) the creation time;
// Task 7 fills them.
func toKey(k store.ClientKey) Key {
	return Key{ID: k.ID, Name: k.Name, Allow: k.Allow, Enabled: k.Enabled}
}

func findClientKey(ctx context.Context, tx *store.Tx, id string) (store.ClientKey, error) {
	keys, err := tx.ClientKeys(ctx)
	if err != nil {
		return store.ClientKey{}, err
	}
	for _, k := range keys {
		if k.ID == id {
			return k, nil
		}
	}
	return store.ClientKey{}, fmt.Errorf("%w: client key %s", store.ErrNotFound, id)
}

func (a keysAdapter) List(ctx context.Context) ([]Key, error) {
	var out []Key
	err := read(ctx, a.st, func(tx *store.Tx) error {
		keys, err := tx.ClientKeys(ctx)
		for _, k := range keys {
			out = append(out, toKey(k))
		}
		return err
	})
	return out, err
}

func (a keysAdapter) Get(ctx context.Context, id string) (Key, error) {
	var out Key
	err := read(ctx, a.st, func(tx *store.Tx) error {
		k, err := findClientKey(ctx, tx, id)
		out = toKey(k)
		return err
	})
	return out, err
}

func (a keysAdapter) Create(ctx context.Context, name string, allow []string, value string) (Key, string, error) {
	if value == "" {
		value = store.GenerateKeyValue()
	}
	var out store.ClientKey
	err := a.rec.Change(ctx, func(ctx context.Context, tx *store.Tx) error {
		var err error
		out, err = tx.PutClientKey(ctx, store.ClientKey{Name: name, Allow: allow, Enabled: true}, value)
		return err
	})
	if err != nil {
		return Key{}, "", err
	}
	return toKey(out), value, nil
}

func (a keysAdapter) Update(ctx context.Context, id string, p KeyPatch) (Key, error) {
	var out store.ClientKey
	err := a.rec.Change(ctx, func(ctx context.Context, tx *store.Tx) error {
		k, err := findClientKey(ctx, tx, id)
		if err != nil {
			return err
		}
		if p.Name != nil {
			k.Name = *p.Name
		}
		if p.Allow != nil {
			k.Allow = *p.Allow
		}
		if p.Enabled != nil {
			k.Enabled = *p.Enabled
		}
		out, err = tx.PutClientKey(ctx, k, "")
		return err
	})
	if err != nil {
		return Key{}, err
	}
	return toKey(out), nil
}

func (a keysAdapter) SetValue(ctx context.Context, id, value string) error {
	if value == "" {
		return badRequest("a key value cannot be empty")
	}
	return a.rec.Change(ctx, func(ctx context.Context, tx *store.Tx) error {
		k, err := findClientKey(ctx, tx, id)
		if err != nil {
			return err
		}
		_, err = tx.PutClientKey(ctx, k, value)
		return err
	})
}

func (a keysAdapter) MatchesValue(ctx context.Context, id, value string) (bool, error) {
	k, err := a.st.ClientKeyByValue(ctx, value)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return k.ID == id, nil
}

func (a keysAdapter) Delete(ctx context.Context, id string) error {
	return a.rec.Change(ctx, func(ctx context.Context, tx *store.Tx) error { return tx.DeleteClientKey(ctx, id) })
}

// NewNames translates engine ids with the ids the reconciler recorded in st.
func NewNames(st *store.Store) EngineNames { return namesAdapter{st: st} }

type namesAdapter struct{ st *store.Store }

func (a namesAdapter) KeyByVK(ctx context.Context, vkID string) (string, string, bool) {
	var id, name string
	_ = read(ctx, a.st, func(tx *store.Tx) error {
		keys, err := tx.ClientKeys(ctx)
		for _, k := range keys {
			if k.VKID == vkID && vkID != "" {
				id, name = k.ID, k.Name
			}
		}
		return err
	})
	return id, name, id != ""
}

func (a namesAdapter) VKByKey(ctx context.Context, keyID string) (string, bool) {
	ids, err := a.st.ClientKeyVKIDs(ctx)
	if err != nil {
		return "", false
	}
	vk, ok := ids[keyID]
	return vk, ok
}

func (a namesAdapter) AliasByRule(ctx context.Context, ruleID string) (string, bool) {
	ids, err := a.st.AliasRuleIDs(ctx)
	if err != nil {
		return "", false
	}
	for name, id := range ids {
		if id == ruleID {
			return name, true
		}
	}
	return "", false
}

func (a namesAdapter) RuleByAlias(ctx context.Context, name string) (string, bool) {
	ids, err := a.st.AliasRuleIDs(ctx)
	if err != nil {
		return "", false
	}
	id, ok := ids[name]
	return id, ok
}

// Target maps an engine provider and model to remora's names. Store providers keep their names in the engine; the
// OAuth engine's internal provider is translated once plan 03 lands.
func (a namesAdapter) Target(_ context.Context, engineProvider, engineModel string) alias.Target {
	return alias.Target{Provider: engineProvider, Model: engineModel}
}

// EngineFilter maps a remora provider and model filter to engine names; see Target.
func (a namesAdapter) EngineFilter(_ context.Context, provider, model string) ([]string, []string) {
	var ps, ms []string
	if provider != "" {
		ps = []string{provider}
	}
	if model != "" {
		ms = []string{model}
	}
	return ps, ms
}
