package api

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/47vigen/remora/internal/accounts"
	"github.com/47vigen/remora/internal/alias"
	"github.com/47vigen/remora/internal/engine/bifrost"
	"github.com/47vigen/remora/internal/quota"
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
	pool      []accounts.PoolStatus
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

func (f *fakeAccounts) Pool(context.Context) ([]accounts.PoolStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pool, nil
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

func (f *fakeKeys) Create(_ context.Context, name string, allow []string, value string, enabled bool) (Key, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.m == nil {
		f.m, f.values = map[string]Key{}, map[string]string{}
	}
	if value == "" {
		value = store.GenerateKeyValue()
	}
	k := Key{
		ID: fmt.Sprintf("k%d", len(f.m)+1), Name: name, Prefix: store.KeyPrefix(value), Allow: allow, Enabled: enabled,
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

// fakeNames translates with fixed tables: keys maps a client key id to its virtual key id, rules an alias name to
// its routing rule id. Target is the real translation (userTarget) unless targets overrides an engine pair.
// EngineFilter prefixes each remora name with "fd-" so tests can see it was translated.
type fakeNames struct {
	keys    map[string]string
	rules   map[string]string
	targets map[[2]string]alias.Target
}

func (f *fakeNames) KeyByVK(_ context.Context, vk string) (string, string, bool) {
	for id, v := range f.keys {
		if v == vk {
			return id, "name-" + id, true
		}
	}
	return "", "", false
}

func (f *fakeNames) VKByKey(_ context.Context, id string) (string, bool) {
	vk, ok := f.keys[id]
	return vk, ok
}

func (f *fakeNames) AliasByRule(_ context.Context, rule string) (string, bool) {
	for name, r := range f.rules {
		if r == rule {
			return name, true
		}
	}
	return "", false
}

func (f *fakeNames) RuleByAlias(_ context.Context, name string) (string, bool) {
	r, ok := f.rules[name]
	return r, ok
}

func (f *fakeNames) Target(_ context.Context, p, m string) alias.Target {
	if t, ok := f.targets[[2]string{p, m}]; ok {
		return t
	}
	return userTarget(p, m)
}

func (f *fakeNames) EngineFilter(_ context.Context, p, m string) ([]string, []string) {
	var ps, ms []string
	if p != "" {
		ps = []string{"fd-" + p}
	}
	if m != "" {
		ms = []string{"fd-" + m}
	}
	return ps, ms
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

// fakeLogs is an in-memory front-door log. SearchLogs filters rows by status, parent, virtual key and routing rule,
// sorts and pages them like the engine, and leaves out error details and bodies as the engine's list does; GetLog
// answers the whole row. It records every filter it is called with.
type fakeLogs struct {
	mu        sync.Mutex
	rows      []bifrost.Log
	stats     func(bifrost.LogFilter) bifrost.LogStats // nil answers zeros
	histogram []bifrost.HistogramBucket
	tokens    []bifrost.TokenBucket
	costs     []bifrost.CostBucket
	latency   []bifrost.LatencyBucket
	usage     []bifrost.ModelUsage
	models    []string // the distinct models LogModels searches

	searches   []bifrost.LogFilter
	statsCalls []bifrost.LogFilter
	histCalls  []bifrost.LogFilter
	latCalls   []bifrost.LogFilter
	usageCalls []bifrost.LogFilter
}

func (f *fakeLogs) add(rows ...bifrost.Log) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows = append(f.rows, rows...)
}

func (f *fakeLogs) lastSearch() (bifrost.LogFilter, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.searches) == 0 {
		return bifrost.LogFilter{}, 0
	}
	return f.searches[len(f.searches)-1], len(f.searches)
}

func anyOf(set []string, v *string) bool {
	if len(set) == 0 {
		return true
	}
	return v != nil && slices.Contains(set, *v)
}

func (f *fakeLogs) SearchLogs(_ context.Context, fl bifrost.LogFilter) (bifrost.LogPage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.searches = append(f.searches, fl)
	var out []bifrost.Log
	for _, l := range f.rows {
		if len(fl.Status) > 0 && !slices.Contains(fl.Status, l.Status) ||
			fl.ParentRequestID != "" && (l.ParentRequestID == nil || *l.ParentRequestID != fl.ParentRequestID) ||
			!anyOf(fl.VirtualKeyIDs, l.VirtualKeyID) || !anyOf(fl.RoutingRuleIDs, l.RoutingRuleID) ||
			fl.Start != nil && l.Timestamp.Before(*fl.Start) || fl.End != nil && l.Timestamp.After(*fl.End) {
			continue
		}
		l.ErrorDetails, l.InputHistory, l.OutputMessage, l.Params = nil, nil, nil, nil
		out = append(out, l)
	}
	desc := fl.Order != "asc"
	slices.SortStableFunc(out, func(a, b bifrost.Log) int {
		var c int
		switch fl.SortBy {
		case "latency":
			c = cmpFloat(a.Latency, b.Latency)
		default:
			c = a.Timestamp.Compare(b.Timestamp)
		}
		if desc {
			c = -c
		}
		return c
	})
	total := int64(len(out))
	limit := fl.Limit
	if limit <= 0 {
		limit = 50
	}
	if fl.Offset >= len(out) {
		return bifrost.LogPage{Logs: []bifrost.Log{}, Total: total}, nil
	}
	out = out[fl.Offset:min(fl.Offset+limit, len(out))]
	return bifrost.LogPage{Logs: slices.Clone(out), Total: total}, nil
}

func cmpFloat(a, b *float64) int {
	var x, y float64
	if a != nil {
		x = *a
	}
	if b != nil {
		y = *b
	}
	switch {
	case x < y:
		return -1
	case x > y:
		return 1
	}
	return 0
}

func (f *fakeLogs) GetLog(_ context.Context, id string) (bifrost.Log, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, l := range f.rows {
		if l.ID == id {
			return l, nil
		}
	}
	return bifrost.Log{}, fmt.Errorf("%w: log %s", store.ErrNotFound, id)
}

func (f *fakeLogs) LogStats(_ context.Context, fl bifrost.LogFilter) (bifrost.LogStats, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statsCalls = append(f.statsCalls, fl)
	if f.stats == nil {
		return bifrost.LogStats{}, nil
	}
	return f.stats(fl), nil
}

func (f *fakeLogs) LogModels(_ context.Context, query string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, m := range f.models {
		if strings.Contains(m, query) {
			out = append(out, m)
		}
	}
	return out, nil
}

func (f *fakeLogs) Histogram(_ context.Context, fl bifrost.LogFilter) ([]bifrost.HistogramBucket, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.histCalls = append(f.histCalls, fl)
	return slices.Clone(f.histogram), nil
}

func (f *fakeLogs) TokenHistogram(context.Context, bifrost.LogFilter) ([]bifrost.TokenBucket, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.tokens), nil
}

func (f *fakeLogs) CostHistogram(context.Context, bifrost.LogFilter) ([]bifrost.CostBucket, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.costs), nil
}

func (f *fakeLogs) LatencyHistogram(_ context.Context, fl bifrost.LogFilter) ([]bifrost.LatencyBucket, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.latCalls = append(f.latCalls, fl)
	return slices.Clone(f.latency), nil
}

func (f *fakeLogs) ModelUsage(_ context.Context, fl bifrost.LogFilter) ([]bifrost.ModelUsage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.usageCalls = append(f.usageCalls, fl)
	return slices.Clone(f.usage), nil
}

// fakeQuota answers overview and records refresh calls; err is returned by Refresh.
type fakeQuota struct {
	mu        sync.Mutex
	overview  []quota.ProviderView
	queued    []string
	skipped   []string
	err       error
	refreshed [][2]string // (account id, provider)
}

func (f *fakeQuota) Overview(context.Context) ([]quota.ProviderView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.overview, nil
}

func (f *fakeQuota) Refresh(_ context.Context, accountID, provider string) ([]string, []string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refreshed = append(f.refreshed, [2]string{accountID, provider})
	return f.queued, f.skipped, f.err
}
