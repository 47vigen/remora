package api

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/47vigen/remora/internal/accounts"
	"github.com/47vigen/remora/internal/alias"
	"github.com/47vigen/remora/internal/cooling"
	"github.com/47vigen/remora/internal/engine/bifrost"
	"github.com/47vigen/remora/internal/engine/cliproxyapi"
	"github.com/47vigen/remora/internal/reconcile"
	"github.com/47vigen/remora/internal/soniox"
	"github.com/47vigen/remora/internal/store"
	"github.com/47vigen/remora/internal/version"
)

// Adapters map the ports onto plans 01–02's store, reconciler and supervisor. Every write goes through
// reconcile.Reconciler.Change, so a change the front door refuses is not committed. Accounts maps plan 03's
// accounts.Service. Not wired yet: the cooling snapshot (cooling.Cooler) and the OAuth engine's provider names in
// EngineNames.Target and EngineFilter.

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

// ModelLister lists models with every key of a front-door provider; *bifrost.Client satisfies it.
type ModelLister interface {
	RefreshProviderModels(ctx context.Context, name string) ([]bifrost.KeyStatus, error)
}

// SonioxFiles lists a Soniox account's files; *soniox.Client satisfies it.
type SonioxFiles interface {
	ListFiles(ctx context.Context, key string) ([]soniox.File, error)
}

// NewProviders manages providers in st through rec. A test lists models through fd, or, for a speech provider,
// lists its files through sx.
func NewProviders(st *store.Store, rec *reconcile.Reconciler, fd ModelLister, sx SonioxFiles) Providers {
	return providersAdapter{st: st, rec: rec, fd: fd, sx: sx}
}

type providersAdapter struct {
	st  *store.Store
	rec *reconcile.Reconciler
	fd  ModelLister
	sx  SonioxFiles
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

// Test calls the upstream with every enabled key of the provider. It is OK when every key answers.
func (a providersAdapter) Test(ctx context.Context, name string) (ProviderTest, error) {
	var (
		p    Provider
		keys []store.ProviderKey
	)
	err := read(ctx, a.st, func(tx *store.Tx) error {
		ps, byProvider, err := loadProviders(ctx, tx)
		if err != nil {
			return err
		}
		i := slices.IndexFunc(ps, func(p Provider) bool { return p.Name == name })
		if i < 0 {
			return fmt.Errorf("%w: provider %s", store.ErrNotFound, name)
		}
		p, keys = ps[i], byProvider[name]
		return nil
	})
	if err != nil {
		return ProviderTest{}, err
	}
	if a.fd == nil || a.sx == nil {
		return ProviderTest{}, errNotImplemented
	}
	start := time.Now()
	var failed []string
	answered := 0
	if p.Kind == string(store.KindSoniox) {
		for _, k := range keys {
			if !k.Enabled {
				continue
			}
			if _, err := a.sx.ListFiles(ctx, k.Secret); err != nil {
				failed = append(failed, k.Name+": "+err.Error())
				continue
			}
			answered++
		}
	} else {
		names := map[string]string{}
		for _, k := range keys {
			names[k.ID] = k.Name
		}
		statuses, err := a.fd.RefreshProviderModels(ctx, name)
		if err != nil {
			return ProviderTest{}, err
		}
		for _, k := range statuses {
			switch k.Status {
			case "success":
				answered++
			case "list_models_failed":
				failed = append(failed, names[k.ID]+": "+k.Description)
			}
		}
	}
	r := ProviderTest{OK: len(failed) == 0 && answered > 0, Latency: time.Since(start)}
	switch {
	case len(failed) > 0:
		r.Message = Scrub(strings.Join(failed, "; "))
	case answered == 0:
		r.Message = "no enabled key could be checked"
	default:
		r.Message = fmt.Sprintf("%d of %d keys answered", answered, len(keys))
	}
	return r, nil
}

// NewAliases manages aliases in st through rec. cooling reports the members cooling now (the cooler's Snapshot);
// nil reports none.
func NewAliases(st *store.Store, rec *reconcile.Reconciler, cooling func() []cooling.Cooling) Aliases {
	return aliasesAdapter{st: st, rec: rec, cooling: cooling}
}

type aliasesAdapter struct {
	st      *store.Store
	rec     *reconcile.Reconciler
	cooling func() []cooling.Cooling
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

// Cooling reports the members cooling now.
func (a aliasesAdapter) Cooling(context.Context) ([]Cooling, error) {
	if a.cooling == nil {
		return nil, nil
	}
	snap := a.cooling()
	out := make([]Cooling, len(snap))
	for i, c := range snap {
		out[i] = Cooling{Target: c.Target, Until: c.Until, Reason: c.Reason}
	}
	return out, nil
}

// NewKeys manages client keys in st through rec.
func NewKeys(st *store.Store, rec *reconcile.Reconciler) Keys { return keysAdapter{st: st, rec: rec} }

type keysAdapter struct {
	st  *store.Store
	rec *reconcile.Reconciler
}

// toKey maps a stored client key.
func toKey(k store.ClientKey) Key {
	return Key{ID: k.ID, Name: k.Name, Prefix: k.Prefix, Allow: k.Allow, Enabled: k.Enabled, CreatedAt: k.CreatedAt}
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

func (a keysAdapter) Create(ctx context.Context, name string, allow []string, value string, enabled bool) (Key, string, error) {
	if value == "" {
		value = store.GenerateKeyValue()
	}
	var out store.ClientKey
	err := a.rec.Change(ctx, func(ctx context.Context, tx *store.Tx) error {
		var err error
		out, err = tx.PutClientKey(ctx, store.ClientKey{Name: name, Allow: allow, Enabled: enabled}, value)
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

// Target maps an engine provider and model to remora's names: store providers keep their names in the engine; the
// OAuth engine's internal provider serves "<name>/<model>", shown as provider <name>.
func (a namesAdapter) Target(_ context.Context, engineProvider, engineModel string) alias.Target {
	return userTarget(engineProvider, engineModel)
}

// EngineFilter maps a remora provider and served-model ("<provider>/<model>") filter to engine names; see Target. A
// subscription provider filters on the OAuth engine's internal provider, whose models carry the provider name.
func (a namesAdapter) EngineFilter(_ context.Context, provider, model string) ([]string, []string) {
	var ps, ms []string
	if provider != "" {
		ps = []string{engineProvider(provider)}
	}
	if model != "" {
		p, m, ok := strings.Cut(model, "/")
		switch {
		case !ok:
			ms = []string{model}
		case isSubscription(p):
			ms = []string{model}
		default:
			ms = []string{m}
		}
		if ok && provider == "" {
			ps = []string{engineProvider(p)}
		}
	}
	return ps, ms
}

// NewAccounts manages subscription accounts through plan 03's accounts service. Account ids are the store's
// integer ids as decimal strings; an id that is not one is not found.
func NewAccounts(svc *accounts.Service) Accounts { return accountsAdapter{svc: svc} }

type accountsAdapter struct{ svc *accounts.Service }

// toAccount maps an account. CooldownUntil is the latest restriction on the whole credential (the engine's next
// retry time or a credential cooldown); each model cooldown becomes a quota entry that resets when it ends, with
// nothing remaining when its reason is quota. The engine's other quota signals are opaque and not shown.
func toAccount(a accounts.Account) Account {
	out := Account{
		ID: strconv.FormatInt(a.ID, 10), Provider: a.Provider, Email: a.Label, Status: a.Status,
		Message: a.StatusMessage, Enabled: a.Enabled, Models: a.Models,
	}
	later := func(t time.Time) {
		if out.CooldownUntil == nil || t.After(*out.CooldownUntil) {
			out.CooldownUntil = &t
		}
	}
	if a.NextRetryAfter != nil {
		later(*a.NextRetryAfter)
	}
	for _, c := range a.Cooldowns {
		if c.Scope != "model" {
			later(c.RetryAt)
			continue
		}
		q := Quota{Model: c.ModelKey, ResetAt: &c.RetryAt}
		if q.Model != "" && !strings.HasPrefix(q.Model, a.Provider+"/") {
			q.Model = a.Provider + "/" + q.Model
		}
		if strings.Contains(strings.ToLower(c.Reason), "quota") {
			q.RemainingFraction = new(float64)
		}
		out.Quota = append(out.Quota, q)
	}
	return out
}

// accountID parses an account id; accounts.ErrNotFound when it is not a positive integer.
func accountID(id string) (int64, error) {
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil || n <= 0 {
		return 0, accounts.ErrNotFound
	}
	return n, nil
}

func (a accountsAdapter) List(ctx context.Context, provider string) ([]Account, error) {
	all, err := a.svc.List(ctx, provider)
	if err != nil {
		return nil, err
	}
	out := make([]Account, 0, len(all))
	for _, acc := range all {
		out = append(out, toAccount(acc))
	}
	return out, nil
}

func (a accountsAdapter) StartLogin(ctx context.Context, provider string) (Login, error) {
	l, err := a.svc.StartLogin(ctx, provider)
	if err != nil {
		return Login{}, err
	}
	return Login{State: l.State, URL: l.URL, UserCode: l.UserCode}, nil
}

func (a accountsAdapter) LoginStatus(ctx context.Context, state string) (LoginStatus, error) {
	st, err := a.svc.LoginStatus(ctx, state)
	if err != nil {
		return LoginStatus{}, err
	}
	return LoginStatus{Status: st.Status, Message: st.Message}, nil
}

func (a accountsAdapter) CompleteLogin(ctx context.Context, state, redirectURL string) error {
	return a.svc.CompleteLogin(ctx, state, redirectURL)
}

func (a accountsAdapter) CancelLogin(ctx context.Context, state string) error {
	return a.svc.CancelLogin(ctx, state)
}

func (a accountsAdapter) SetEnabled(ctx context.Context, id string, enabled bool) error {
	n, err := accountID(id)
	if err != nil {
		return err
	}
	return a.svc.SetEnabled(ctx, n, enabled)
}

func (a accountsAdapter) Delete(ctx context.Context, id string) error {
	n, err := accountID(id)
	if err != nil {
		return err
	}
	return a.svc.Delete(ctx, n)
}

func (a accountsAdapter) ResetCooldown(ctx context.Context, id string) error {
	n, err := accountID(id)
	if err != nil {
		return err
	}
	return a.svc.ResetCooldown(ctx, n)
}

func (a accountsAdapter) Import(ctx context.Context, provider string, credential []byte) (Account, error) {
	acc, err := a.svc.Import(ctx, provider, credential)
	if err != nil {
		return Account{}, err
	}
	return toAccount(acc), nil
}

// isSubscription reports whether name is a subscription provider served by the OAuth engine.
func isSubscription(name string) bool {
	_, ok := cliproxyapi.ProviderByName(name)
	return ok
}

// engineProvider is the front-door provider that serves a remora provider.
func engineProvider(name string) string {
	if isSubscription(name) {
		return bifrost.OAuthProvider
	}
	return name
}
