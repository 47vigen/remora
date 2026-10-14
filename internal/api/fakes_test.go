package api

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/47vigen/remora/internal/accounts"
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

// fakeAccounts records the calls it gets. StartLogin answers next (state "s1" by default) and registers it as
// waiting; CompleteLogin turns a login "ok".
type fakeAccounts struct {
	mu        sync.Mutex
	accounts  []Account
	next      Login
	logins    map[string]string // state → status
	completed [][2]string       // (state, redirect URL)
	cancelled []string
	enabled   map[string]bool // id → last SetEnabled
	reset     []string
	imported  []fakeImport
}

type fakeImport struct {
	provider   string
	credential []byte
}

func (f *fakeAccounts) List(_ context.Context, provider string) ([]Account, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []Account{}
	for _, a := range f.accounts {
		if provider == "" || a.Provider == provider {
			out = append(out, a)
		}
	}
	return out, nil
}

func (f *fakeAccounts) StartLogin(_ context.Context, provider string) (Login, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if provider != "codex" && provider != "grok" && provider != "antigravity" {
		return Login{}, fmt.Errorf("%w: %q", accounts.ErrUnknownProvider, provider)
	}
	l := f.next
	if l.State == "" {
		l = Login{State: "s1", URL: "https://login.example/authorize?state=s1"}
	}
	if f.logins == nil {
		f.logins = map[string]string{}
	}
	f.logins[l.State] = "wait"
	return l, nil
}

func (f *fakeAccounts) LoginStatus(_ context.Context, state string) (LoginStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	st, ok := f.logins[state]
	if !ok {
		return LoginStatus{}, accounts.ErrLoginNotFound
	}
	return LoginStatus{Status: st}, nil
}

func (f *fakeAccounts) CompleteLogin(_ context.Context, state, redirectURL string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.completed = append(f.completed, [2]string{state, redirectURL})
	if _, ok := f.logins[state]; !ok {
		return accounts.ErrLoginNotFound
	}
	f.logins[state] = "ok"
	return nil
}

func (f *fakeAccounts) CancelLogin(_ context.Context, state string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.logins[state]; !ok {
		return accounts.ErrLoginNotFound
	}
	f.cancelled = append(f.cancelled, state)
	delete(f.logins, state)
	return nil
}

// find returns the index of an account; f.mu is held.
func (f *fakeAccounts) find(id string) (int, error) {
	i := slices.IndexFunc(f.accounts, func(a Account) bool { return a.ID == id })
	if i < 0 {
		return 0, accounts.ErrNotFound
	}
	return i, nil
}

func (f *fakeAccounts) SetEnabled(_ context.Context, id string, enabled bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	i, err := f.find(id)
	if err != nil {
		return err
	}
	f.accounts[i].Enabled = enabled
	if f.enabled == nil {
		f.enabled = map[string]bool{}
	}
	f.enabled[id] = enabled
	return nil
}

func (f *fakeAccounts) Delete(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	i, err := f.find(id)
	if err != nil {
		return err
	}
	f.accounts = slices.Delete(f.accounts, i, i+1)
	return nil
}

func (f *fakeAccounts) ResetCooldown(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	i, err := f.find(id)
	if err != nil {
		return err
	}
	f.accounts[i].CooldownUntil = nil
	f.reset = append(f.reset, id)
	return nil
}

// Import refuses a credential whose type names another provider, as the accounts service does.
func (f *fakeAccounts) Import(_ context.Context, provider string, credential []byte) (Account, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.imported = append(f.imported, fakeImport{provider, slices.Clone(credential)})
	var doc struct {
		Type, Email string
	}
	if err := json.Unmarshal(credential, &doc); err != nil {
		return Account{}, fmt.Errorf("%w: it must be a JSON object", accounts.ErrBadCredential)
	}
	if doc.Type != "" && doc.Type != provider {
		return Account{}, fmt.Errorf("%w: it is not a %s credential", accounts.ErrBadCredential, provider)
	}
	a := Account{ID: fmt.Sprint(len(f.accounts) + 100), Provider: provider, Email: doc.Email, Enabled: true, Status: "active"}
	f.accounts = append(f.accounts, a)
	return a, nil
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
	k := Key{
		ID: fmt.Sprintf("k%d", len(f.m)+1), Name: name, Prefix: store.KeyPrefix(value), Allow: allow, Enabled: true,
		CreatedAt: time.Now().UTC(),
	}
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
	if _, ok := f.m[id]; !ok {
		return fmt.Errorf("%w: key %s", store.ErrNotFound, id)
	}
	delete(f.m, id)
	delete(f.values, id)
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
