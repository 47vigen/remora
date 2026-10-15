package config

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/47vigen/remora/internal/alias"
)

var (
	errNotFound = errors.New("not found")
	errFake     = errors.New("fake failure")
)

// world is an in-memory store behind the three ports. Every write is logged as "<verb> <kind> <name>"; a write
// whose log entry equals failOn fails instead.
type world struct {
	mu        sync.Mutex
	providers map[string]Provider
	aliases   map[string]alias.Alias
	keys      map[string]Key // by id
	values    map[string]string
	nextID    int
	log       []string
	failOn    string
}

func newWorld() *world {
	return &world{providers: map[string]Provider{}, aliases: map[string]alias.Alias{}, keys: map[string]Key{}, values: map[string]string{}}
}

func (w *world) service() *Service { return NewService(fakeProviders{w}, fakeAliases{w}, fakeKeys{w}) }

// write logs a write and reports the injected failure. The caller holds w.mu.
func (w *world) write(entry string) error {
	if entry == w.failOn {
		return fmt.Errorf("%s: %w", entry, errFake)
	}
	w.log = append(w.log, entry)
	return nil
}

func (w *world) calls() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.log)
}

func (w *world) keyByName(name string) (Key, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, k := range w.keys {
		if k.Name == name {
			return k, true
		}
	}
	return Key{}, false
}

type fakeProviders struct{ w *world }

func (f fakeProviders) List(context.Context) ([]Provider, error) {
	f.w.mu.Lock()
	defer f.w.mu.Unlock()
	return slices.SortedFunc(maps.Values(f.w.providers), func(a, b Provider) int { return cmp.Compare(a.Name, b.Name) }), nil
}

func (f fakeProviders) Get(_ context.Context, name string) (Provider, error) {
	f.w.mu.Lock()
	defer f.w.mu.Unlock()
	p, ok := f.w.providers[name]
	if !ok {
		return Provider{}, errNotFound
	}
	return p, nil
}

func (f fakeProviders) Create(_ context.Context, p Provider) (Provider, error) {
	f.w.mu.Lock()
	defer f.w.mu.Unlock()
	if _, ok := f.w.providers[p.Name]; ok {
		return Provider{}, errors.New("duplicate")
	}
	for _, k := range p.Keys {
		if k.Value == "" {
			return Provider{}, fmt.Errorf("key %s without a value", k.Name)
		}
	}
	if err := f.w.write("create provider " + p.Name); err != nil {
		return Provider{}, err
	}
	f.w.providers[p.Name] = p
	return p, nil
}

// Update follows the port: empty Keys keep the stored keys; otherwise keys are replaced by name and an empty
// Value keeps the stored secret.
func (f fakeProviders) Update(_ context.Context, p Provider) (Provider, error) {
	f.w.mu.Lock()
	defer f.w.mu.Unlock()
	old, ok := f.w.providers[p.Name]
	if !ok {
		return Provider{}, errNotFound
	}
	if err := f.w.write("update provider " + p.Name); err != nil {
		return Provider{}, err
	}
	if len(p.Keys) == 0 {
		p.Keys = old.Keys
	} else {
		keys := make([]ProviderKey, 0, len(p.Keys))
		for _, k := range p.Keys {
			if k.Value == "" {
				i := slices.IndexFunc(old.Keys, func(o ProviderKey) bool { return o.Name == k.Name })
				if i < 0 {
					return Provider{}, fmt.Errorf("key %s without a value", k.Name)
				}
				k.Value = old.Keys[i].Value
			}
			keys = append(keys, k)
		}
		p.Keys = keys
	}
	f.w.providers[p.Name] = p
	return p, nil
}

func (f fakeProviders) Delete(_ context.Context, name string) error {
	f.w.mu.Lock()
	defer f.w.mu.Unlock()
	if _, ok := f.w.providers[name]; !ok {
		return errNotFound
	}
	if err := f.w.write("delete provider " + name); err != nil {
		return err
	}
	delete(f.w.providers, name)
	return nil
}

func (f fakeProviders) Test(context.Context, string) (ProviderTest, error) {
	return ProviderTest{OK: true}, nil
}

type fakeAliases struct{ w *world }

func (f fakeAliases) List(context.Context) ([]alias.Alias, error) {
	f.w.mu.Lock()
	defer f.w.mu.Unlock()
	return slices.SortedFunc(maps.Values(f.w.aliases), func(a, b alias.Alias) int { return cmp.Compare(a.Name, b.Name) }), nil
}

func (f fakeAliases) Get(_ context.Context, name string) (alias.Alias, error) {
	f.w.mu.Lock()
	defer f.w.mu.Unlock()
	a, ok := f.w.aliases[name]
	if !ok {
		return alias.Alias{}, errNotFound
	}
	return a, nil
}

// Put checks that every member is a stored alias or names a provider, as the real adapter's compile does.
func (f fakeAliases) Put(_ context.Context, a alias.Alias, create bool) (alias.Alias, error) {
	f.w.mu.Lock()
	defer f.w.mu.Unlock()
	if _, ok := f.w.aliases[a.Name]; ok == create {
		return alias.Alias{}, fmt.Errorf("alias %s: exists=%v", a.Name, ok)
	}
	for _, m := range a.Members {
		provider, _, isTarget := strings.Cut(m.Ref, "/")
		_, isAlias := f.w.aliases[m.Ref]
		_, isProvider := f.w.providers[provider]
		if !isAlias && (!isTarget || !isProvider) {
			return alias.Alias{}, fmt.Errorf("%w: %s", alias.ErrUnknownMember, m.Ref)
		}
	}
	verb := "update"
	if create {
		verb = "create"
	}
	if err := f.w.write(verb + " alias " + a.Name); err != nil {
		return alias.Alias{}, err
	}
	f.w.aliases[a.Name] = a
	return a, nil
}

func (f fakeAliases) Delete(_ context.Context, name string) error {
	f.w.mu.Lock()
	defer f.w.mu.Unlock()
	if _, ok := f.w.aliases[name]; !ok {
		return errNotFound
	}
	if err := f.w.write("delete alias " + name); err != nil {
		return err
	}
	delete(f.w.aliases, name)
	return nil
}

func (f fakeAliases) Chain(context.Context, string) ([]alias.Target, error) { return nil, nil }

func (f fakeAliases) Compile(context.Context, alias.Alias) ([]alias.Target, error) { return nil, nil }

func (f fakeAliases) Cooling(context.Context) ([]CoolingMember, error) { return nil, nil }

type fakeKeys struct{ w *world }

func (f fakeKeys) List(context.Context) ([]Key, error) {
	f.w.mu.Lock()
	defer f.w.mu.Unlock()
	return slices.SortedFunc(maps.Values(f.w.keys), func(a, b Key) int { return cmp.Compare(a.Name, b.Name) }), nil
}

func (f fakeKeys) Get(_ context.Context, id string) (Key, error) {
	f.w.mu.Lock()
	defer f.w.mu.Unlock()
	k, ok := f.w.keys[id]
	if !ok {
		return Key{}, errNotFound
	}
	return k, nil
}

func (f fakeKeys) Create(_ context.Context, name string, allow []string, value string, enabled bool) (Key, string, error) {
	f.w.mu.Lock()
	defer f.w.mu.Unlock()
	if err := f.w.write("create key " + name); err != nil {
		return Key{}, "", err
	}
	if value == "" {
		value = fmt.Sprintf("sk-generated-%d", f.w.nextID)
	}
	f.w.nextID++
	k := Key{ID: fmt.Sprintf("id-%d", f.w.nextID), Name: name, Allow: allow, Enabled: enabled}
	f.w.keys[k.ID], f.w.values[k.ID] = k, value
	return k, value, nil
}

func (f fakeKeys) Update(_ context.Context, id string, p KeyPatch) (Key, error) {
	f.w.mu.Lock()
	defer f.w.mu.Unlock()
	k, ok := f.w.keys[id]
	if !ok {
		return Key{}, errNotFound
	}
	if err := f.w.write("update key " + k.Name); err != nil {
		return Key{}, err
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
	f.w.keys[id] = k
	return k, nil
}

func (f fakeKeys) SetValue(_ context.Context, id, value string) error {
	f.w.mu.Lock()
	defer f.w.mu.Unlock()
	k, ok := f.w.keys[id]
	if !ok {
		return errNotFound
	}
	if err := f.w.write("set key " + k.Name); err != nil {
		return err
	}
	f.w.values[id] = value
	return nil
}

func (f fakeKeys) MatchesValue(_ context.Context, id, value string) (bool, error) {
	f.w.mu.Lock()
	defer f.w.mu.Unlock()
	return f.w.values[id] == value, nil
}

func (f fakeKeys) Delete(_ context.Context, id string) error {
	f.w.mu.Lock()
	defer f.w.mu.Unlock()
	k, ok := f.w.keys[id]
	if !ok {
		return errNotFound
	}
	if err := f.w.write("delete key " + k.Name); err != nil {
		return err
	}
	delete(f.w.keys, id)
	delete(f.w.values, id)
	return nil
}

func members(refs ...string) []alias.Member {
	out := make([]alias.Member, len(refs))
	for i, r := range refs {
		out[i] = alias.Member{Ref: r}
	}
	return out
}

// populated is a world with two providers, nested aliases and three client keys, one disabled.
func populated() *world {
	w := newWorld()
	w.providers["openrouter"] = Provider{
		Name: "openrouter", Kind: "builtin", Type: "openrouter",
		Keys: []ProviderKey{{Name: "main", Value: "sk-or-stored-secret"}},
	}
	w.providers["up-a"] = Provider{
		Name: "up-a", Kind: "openai-compatible", BaseURL: "http://upstream.test/a", AllowPrivateNetwork: true,
		Keys: []ProviderKey{{Name: "main", Value: "sk-up-stored-secret"}, {Name: "backup", Value: "sk-up-stored-backup"}},
	}
	w.aliases["inner"] = alias.Alias{Name: "inner", Kind: alias.KindChat, Members: members("up-a/m1"), Strategy: alias.StrategyRoundRobin}
	w.aliases["fast"] = alias.Alias{
		Name: "fast", Kind: alias.KindChat, Members: members("inner", "openrouter/vendor/model-a"),
		Capabilities: []string{"structured_outputs", "tools"}, ContextWindow: 131072,
	}
	w.aliases["emb"] = alias.Alias{Name: "emb", Kind: alias.KindEmbeddings, Members: members("up-a/emb-1")}
	for i, k := range []Key{
		{Name: "full", Allow: []string{"*"}, Enabled: true},
		{Name: "limited", Allow: []string{"fast", "up-a/*"}, Enabled: true},
		{Name: "off", Allow: []string{"*"}, Enabled: false},
	} {
		k.ID = fmt.Sprintf("stored-%d", i)
		w.keys[k.ID], w.values[k.ID] = k, "sk-client-stored-"+k.Name
	}
	return w
}
