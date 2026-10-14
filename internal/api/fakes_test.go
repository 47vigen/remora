package api

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sync"

	"github.com/47vigen/remora/internal/alias"
	"github.com/47vigen/remora/internal/store"
)

// In-memory port fakes. Later tasks extend them as their handlers need.

type fakeProviders struct {
	mu   sync.Mutex
	m    map[string]Provider
	test map[string]ProviderTest // Test's answer per provider; OK when absent
}

func (f *fakeProviders) setTest(name string, r ProviderTest) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.test == nil {
		f.test = map[string]ProviderTest{}
	}
	f.test[name] = r
}

func (f *fakeProviders) List(context.Context) ([]Provider, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.SortedFunc(maps.Values(f.m), func(a, b Provider) int { return cmpString(a.Name, b.Name) }), nil
}

func (f *fakeProviders) Get(_ context.Context, name string) (Provider, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.m[name]
	if !ok {
		return Provider{}, fmt.Errorf("%w: provider %s", store.ErrNotFound, name)
	}
	return p, nil
}

func (f *fakeProviders) Create(_ context.Context, p Provider) (Provider, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.m[p.Name]; ok {
		return Provider{}, fmt.Errorf("%w: provider %s", store.ErrDuplicate, p.Name)
	}
	if f.m == nil {
		f.m = map[string]Provider{}
	}
	f.m[p.Name] = p
	return p, nil
}

func (f *fakeProviders) Update(_ context.Context, p Provider) (Provider, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	old, ok := f.m[p.Name]
	if !ok {
		return Provider{}, fmt.Errorf("%w: provider %s", store.ErrNotFound, p.Name)
	}
	if len(p.Keys) == 0 {
		p.Keys = old.Keys
	}
	f.m[p.Name] = p
	return p, nil
}

func (f *fakeProviders) Delete(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.m[name]; !ok {
		return fmt.Errorf("%w: provider %s", store.ErrNotFound, name)
	}
	delete(f.m, name)
	return nil
}

func (f *fakeProviders) Test(_ context.Context, name string) (ProviderTest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.m[name]; !ok {
		return ProviderTest{}, fmt.Errorf("%w: provider %s", store.ErrNotFound, name)
	}
	if r, ok := f.test[name]; ok {
		return r, nil
	}
	return ProviderTest{OK: true}, nil
}

type fakeAccounts struct{}

func (fakeAccounts) List(context.Context, string) ([]Account, error)   { return nil, nil }
func (fakeAccounts) StartLogin(context.Context, string) (Login, error) { return Login{}, nil }
func (fakeAccounts) LoginStatus(context.Context, string) (LoginStatus, error) {
	return LoginStatus{Status: "wait"}, nil
}
func (fakeAccounts) CompleteLogin(context.Context, string, string) error { return nil }
func (fakeAccounts) CancelLogin(context.Context, string) error           { return nil }
func (fakeAccounts) SetEnabled(context.Context, string, bool) error      { return nil }
func (fakeAccounts) Delete(context.Context, string) error                { return nil }
func (fakeAccounts) ResetCooldown(context.Context, string) error         { return nil }
func (fakeAccounts) Import(context.Context, string, []byte) (Account, error) {
	return Account{}, nil
}

type fakeAliases struct {
	mu      sync.Mutex
	m       map[string]alias.Alias
	putErr  error // returned by Put when set
	cooling []Cooling
}

func (f *fakeAliases) setCooling(c ...Cooling) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cooling = c
}

func (f *fakeAliases) List(context.Context) ([]alias.Alias, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.SortedFunc(maps.Values(f.m), func(a, b alias.Alias) int { return cmpString(a.Name, b.Name) }), nil
}

func (f *fakeAliases) Get(_ context.Context, name string) (alias.Alias, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.m[name]
	if !ok {
		return alias.Alias{}, fmt.Errorf("%w: alias %s", store.ErrNotFound, name)
	}
	return a, nil
}

func (f *fakeAliases) Put(_ context.Context, a alias.Alias, create bool) (alias.Alias, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.putErr != nil {
		return alias.Alias{}, f.putErr
	}
	if _, ok := f.m[a.Name]; ok == create {
		if create {
			return alias.Alias{}, fmt.Errorf("%w: alias %s", store.ErrDuplicate, a.Name)
		}
		return alias.Alias{}, fmt.Errorf("%w: alias %s", store.ErrNotFound, a.Name)
	}
	if f.m == nil {
		f.m = map[string]alias.Alias{}
	}
	f.m[a.Name] = a
	return a, nil
}

func (f *fakeAliases) Delete(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.m[name]; !ok {
		return fmt.Errorf("%w: alias %s", store.ErrNotFound, name)
	}
	delete(f.m, name)
	return nil
}

func (f *fakeAliases) Chain(_ context.Context, name string) ([]alias.Target, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.m[name]; !ok {
		return nil, fmt.Errorf("%w: alias %s", store.ErrNotFound, name)
	}
	return alias.Compile(f.m, name, 0)
}

func (f *fakeAliases) Compile(_ context.Context, a alias.Alias) ([]alias.Target, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	all := maps.Clone(f.m)
	if all == nil {
		all = map[string]alias.Alias{}
	}
	all[a.Name] = a
	return alias.Compile(all, a.Name, 0)
}

func (f *fakeAliases) Cooling(context.Context) ([]Cooling, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.cooling), nil
}

type fakeKeys struct {
	mu     sync.Mutex
	m      map[string]Key
	values map[string]string
}

func (f *fakeKeys) List(context.Context) ([]Key, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.SortedFunc(maps.Values(f.m), func(a, b Key) int { return cmpString(a.Name, b.Name) }), nil
}

func (f *fakeKeys) Get(_ context.Context, id string) (Key, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k, ok := f.m[id]
	if !ok {
		return Key{}, fmt.Errorf("%w: key %s", store.ErrNotFound, id)
	}
	return k, nil
}

func (f *fakeKeys) Create(_ context.Context, name string, allow []string, value string) (Key, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.m == nil {
		f.m, f.values = map[string]Key{}, map[string]string{}
	}
	if value == "" {
		value = store.GenerateKeyValue()
	}
	k := Key{ID: fmt.Sprintf("k%d", len(f.m)+1), Name: name, Allow: allow, Enabled: true}
	f.m[k.ID], f.values[k.ID] = k, value
	return k, value, nil
}

func (f *fakeKeys) Update(_ context.Context, id string, p KeyPatch) (Key, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k, ok := f.m[id]
	if !ok {
		return Key{}, fmt.Errorf("%w: key %s", store.ErrNotFound, id)
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
	f.m[id] = k
	return k, nil
}

func (f *fakeKeys) SetValue(_ context.Context, id, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.values[id] = value
	return nil
}

func (f *fakeKeys) MatchesValue(_ context.Context, id, value string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.values[id] == value, nil
}

func (f *fakeKeys) Delete(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.m, id)
	return nil
}

// fakeSystem reports one component, "front-door", healthy unless err is set.
type fakeSystem struct {
	mu  sync.Mutex
	err error
}

func (f *fakeSystem) setErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

func (f *fakeSystem) Health(context.Context) (bool, map[string]error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.err == nil, map[string]error{"front-door": f.err}
}

func (f *fakeSystem) Version() (string, []EngineVersion) {
	return "v0.0.0-test", []EngineVersion{{Name: "accounts-engine", Version: "v2"}, {Name: "front-door", Version: "v1"}}
}

type fakeNames struct{}

func (fakeNames) KeyByVK(context.Context, string) (string, string, bool) { return "", "", false }
func (fakeNames) VKByKey(context.Context, string) (string, bool)         { return "", false }
func (fakeNames) AliasByRule(context.Context, string) (string, bool)     { return "", false }
func (fakeNames) RuleByAlias(context.Context, string) (string, bool)     { return "", false }
func (fakeNames) Target(_ context.Context, p, m string) alias.Target {
	return alias.Target{Provider: p, Model: m}
}

func (fakeNames) EngineFilter(_ context.Context, p, m string) ([]string, []string) {
	return []string{p}, []string{m}
}

func cmpString(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
