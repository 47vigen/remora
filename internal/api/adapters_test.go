package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/47vigen/remora/internal/accounts"
	"github.com/47vigen/remora/internal/alias"
	"github.com/47vigen/remora/internal/config"
	"github.com/47vigen/remora/internal/cooling"
	"github.com/47vigen/remora/internal/engine/bifrost"
	"github.com/47vigen/remora/internal/engine/cliproxyapi"
	"github.com/47vigen/remora/internal/engine/cliproxyapi/cliproxyapitest"
	"github.com/47vigen/remora/internal/reconcile"
	"github.com/47vigen/remora/internal/reconcile/reconciletest"
	"github.com/47vigen/remora/internal/soniox"
	"github.com/47vigen/remora/internal/store"
)

func adapterStore(t *testing.T) (*store.Store, *reconcile.Reconciler, *reconciletest.Engine) {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "remora.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	sl, err := store.NewSealer(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	st.SetSealer(sl)
	eng := reconciletest.New()
	return st, &reconcile.Reconciler{Store: st, Engine: eng}, eng
}

func TestProvidersAdapter(t *testing.T) {
	ctx := context.Background()
	st, rec, _ := adapterStore(t)
	ps := NewProviders(st, rec, nil, nil)

	p := Provider{
		Name: "up", Kind: "openai-compatible", BaseURL: "http://up.test/v1",
		Keys: []ProviderKey{{Name: "main", Value: "k1"}, {Name: "spare", Value: "k2"}},
	}
	if _, err := ps.Create(ctx, p); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := ps.Create(ctx, p); !errors.Is(err, store.ErrDuplicate) {
		t.Errorf("second Create err = %v, want ErrDuplicate", err)
	}
	got, err := ps.Update(ctx, Provider{Name: "up", Kind: "openai-compatible", BaseURL: "http://up.test/v2"})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got.BaseURL != "http://up.test/v2" || len(got.Keys) != 2 || got.Keys[0].Value != "k1" {
		t.Errorf("after update without keys = %+v", got)
	}
	got, err = ps.Update(ctx, Provider{
		Name: "up", Kind: "openai-compatible", BaseURL: "http://up.test/v2",
		Keys: []ProviderKey{{Name: "main"}},
	})
	if err != nil {
		t.Fatalf("Update keys: %v", err)
	}
	if len(got.Keys) != 1 || got.Keys[0] != (ProviderKey{Name: "main", Value: "k1"}) {
		t.Errorf("after replacing keys = %+v", got.Keys)
	}
	if _, err := ps.Update(ctx, Provider{Name: "nope", Kind: "openai-compatible", BaseURL: "http://x.test"}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Update(nope) err = %v, want ErrNotFound", err)
	}
	if err := ps.Delete(ctx, "up"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := ps.Get(ctx, "up"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Get after delete err = %v", err)
	}
}

func TestAliasesAndNamesAdapters(t *testing.T) {
	ctx := context.Background()
	st, rec, _ := adapterStore(t)
	if _, err := NewProviders(st, rec, nil, nil).Create(ctx, Provider{
		Name: "up", Kind: "openai-compatible",
		BaseURL: "http://up.test/v1", Keys: []ProviderKey{{Name: "main", Value: "k1"}},
	}); err != nil {
		t.Fatalf("Create provider: %v", err)
	}
	as := NewAliases(st, rec, nil)
	fast := alias.Alias{Name: "fast", Kind: alias.KindChat, Members: []alias.Member{{Ref: "up/m1"}, {Ref: "up/m2"}}}
	if _, err := as.Put(ctx, fast, true); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if _, err := as.Put(ctx, fast, true); !errors.Is(err, store.ErrDuplicate) {
		t.Errorf("second create err = %v, want ErrDuplicate", err)
	}
	cyc := alias.Alias{Name: "fast", Kind: alias.KindChat, Members: []alias.Member{{Ref: "fast"}}}
	if _, err := as.Put(ctx, cyc, false); !errors.Is(err, alias.ErrCycle) {
		t.Errorf("cyclic update err = %v, want ErrCycle", err)
	}
	chain, err := as.Chain(ctx, "fast")
	if err != nil || !slices.Equal(chain, []alias.Target{{Provider: "up", Model: "m1"}, {Provider: "up", Model: "m2"}}) {
		t.Errorf("Chain = %v, %v", chain, err)
	}
	if _, err := as.Chain(ctx, "nope"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Chain(nope) err = %v, want ErrNotFound", err)
	}
	draft := alias.Alias{Name: "draft", Kind: alias.KindChat, Members: []alias.Member{{Ref: "fast"}}}
	if got, err := as.Compile(ctx, draft); err != nil || len(got) != 2 {
		t.Errorf("Compile = %v, %v", got, err)
	}
	if _, err := as.Get(ctx, "draft"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Compile stored the draft: %v", err)
	}

	names := NewNames(st)
	rule, ok := names.RuleByAlias(ctx, "fast")
	if !ok || rule == "" {
		t.Fatalf("RuleByAlias = %q, %v", rule, ok)
	}
	if name, ok := names.AliasByRule(ctx, rule); !ok || name != "fast" {
		t.Errorf("AliasByRule = %q, %v", name, ok)
	}
	if err := as.Delete(ctx, "fast"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

func TestKeysAdapter(t *testing.T) {
	ctx := context.Background()
	st, rec, _ := adapterStore(t)
	ks := NewKeys(st, rec)

	k, value, err := ks.Create(ctx, "svc", []string{"fast"}, "", true)
	if err != nil || value == "" || k.ID == "" || !k.Enabled {
		t.Fatalf("Create = %+v %q %v", k, value, err)
	}
	if k.Prefix != value[:8] || k.CreatedAt.IsZero() {
		t.Errorf("Create prefix, created_at = %q, %v; want %q, set", k.Prefix, k.CreatedAt, value[:8])
	}
	if got, err := ks.Get(ctx, k.ID); err != nil || got.Prefix != k.Prefix || !got.CreatedAt.Equal(k.CreatedAt) {
		t.Errorf("Get = %+v, %v", got, err)
	}
	if ok, err := ks.MatchesValue(ctx, k.ID, value); err != nil || !ok {
		t.Errorf("MatchesValue = %v, %v", ok, err)
	}
	off := false
	got, err := ks.Update(ctx, k.ID, KeyPatch{Enabled: &off})
	if err != nil || got.Enabled || !slices.Equal(got.Allow, []string{"fast"}) {
		t.Errorf("Update = %+v, %v", got, err)
	}
	if err := ks.SetValue(ctx, k.ID, "sk-imported-123"); err != nil {
		t.Fatalf("SetValue: %v", err)
	}
	if ok, _ := ks.MatchesValue(ctx, k.ID, "sk-imported-123"); !ok {
		t.Error("the new value does not match")
	}
	if ok, _ := ks.MatchesValue(ctx, k.ID, value); ok {
		t.Error("the old value still matches")
	}

	names := NewNames(st)
	vk, ok := names.VKByKey(ctx, k.ID)
	if !ok || vk == "" {
		t.Fatalf("VKByKey = %q, %v", vk, ok)
	}
	if id, name, ok := names.KeyByVK(ctx, vk); !ok || id != k.ID || name != "svc" {
		t.Errorf("KeyByVK = %q %q %v", id, name, ok)
	}

	if err := ks.Delete(ctx, k.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := ks.Get(ctx, k.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Get after delete err = %v", err)
	}
}

const testOAuthSecret = "test-oauth-management-secret" //nolint:gosec // G101: a test value for the fake engine.

// newAccountsAdapter is the Accounts port over a real accounts service, a fresh store and a fake OAuth engine.
func newAccountsAdapter(t *testing.T) (Accounts, *cliproxyapitest.Fake, *store.Store) {
	t.Helper()
	st, _, _ := adapterStore(t)
	fake := cliproxyapitest.NewServer(t, testOAuthSecret)
	svc := accounts.New(cliproxyapi.New(fake.URL, testOAuthSecret, nil), st, nil)
	return NewAccounts(svc), fake, st
}

// addAccountRow stores an account row; file is the engine credential's file name.
func addAccountRow(t *testing.T, st *store.Store, provider, label, file string, enabled bool) store.Account {
	t.Helper()
	row, err := st.CreateAccount(context.Background(), store.Account{
		Provider: provider, Label: label, Credential: file, Enabled: enabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	return row
}

func TestAccountsAdapterList(t *testing.T) {
	ctx := context.Background()
	acc, fake, st := newAccountsAdapter(t)
	retry := time.Date(2026, 10, 14, 13, 0, 0, 0, time.UTC)
	later := retry.Add(time.Hour)
	fake.AddCredential(cliproxyapi.Credential{
		Name: "codex-a.json", Provider: "codex", Email: "a@example.com", Status: "error", StatusMessage: "quota exhausted",
		Unavailable: true, NextRetryAfter: &retry,
		Cooldowns: []cliproxyapi.Cooldown{
			{Scope: "model", ModelKey: "gpt-x", Reason: "quota", RetryAt: retry, HTTPStatus: 429},
			{Scope: "credential", Reason: "rate_limit", RetryAt: later, HTTPStatus: 429},
		},
	})
	fake.SetModels("codex-a.json", "gpt-x")
	row := addAccountRow(t, st, "codex", "a@example.com", "codex-a.json", true)
	addAccountRow(t, st, "grok", "g@example.com", "xai-g.json", false)

	got, err := acc.List(ctx, "codex")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("List(codex) = %+v", got)
	}
	a := got[0]
	if a.ID != strconv.FormatInt(row.ID, 10) || a.Provider != "codex" || a.Email != "a@example.com" || !a.Enabled ||
		a.Status != "error" || a.Message != "quota exhausted" || !slices.Equal(a.Models, []string{"codex/gpt-x"}) {
		t.Errorf("account = %+v", a)
	}
	if a.CooldownUntil == nil || !a.CooldownUntil.Equal(later) {
		t.Errorf("CooldownUntil = %v, want %v (the latest credential restriction)", a.CooldownUntil, later)
	}
	if len(a.Quota) != 1 || a.Quota[0].Model != "codex/gpt-x" || a.Quota[0].RemainingFraction == nil ||
		*a.Quota[0].RemainingFraction != 0 || a.Quota[0].ResetAt == nil || !a.Quota[0].ResetAt.Equal(retry) {
		t.Errorf("Quota = %+v", a.Quota)
	}

	all, err := acc.List(ctx, "")
	if err != nil || len(all) != 2 || all[1].Status != "missing" || all[1].Enabled {
		t.Errorf("List() = %+v, %v", all, err)
	}
}

func TestAccountsAdapterIDs(t *testing.T) {
	ctx := context.Background()
	acc, fake, st := newAccountsAdapter(t)
	fake.AddCredential(cliproxyapi.Credential{Name: "codex-a.json", Provider: "codex"})
	row := addAccountRow(t, st, "codex", "", "codex-a.json", true)
	id := strconv.FormatInt(row.ID, 10)
	if err := acc.SetEnabled(ctx, id, false); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}
	if c, _, _ := fake.Credential("codex-a.json"); !c.Disabled {
		t.Error("engine credential still enabled")
	}
	if err := acc.ResetCooldown(ctx, id); err != nil {
		t.Errorf("ResetCooldown: %v", err)
	}
	for _, bad := range []string{"abc", "", "999", "-1"} {
		if err := acc.SetEnabled(ctx, bad, true); !errors.Is(err, accounts.ErrNotFound) {
			t.Errorf("SetEnabled(%q) err = %v, want ErrNotFound", bad, err)
		}
		if err := acc.Delete(ctx, bad); !errors.Is(err, accounts.ErrNotFound) {
			t.Errorf("Delete(%q) err = %v, want ErrNotFound", bad, err)
		}
		if err := acc.ResetCooldown(ctx, bad); !errors.Is(err, accounts.ErrNotFound) {
			t.Errorf("ResetCooldown(%q) err = %v, want ErrNotFound", bad, err)
		}
	}
	if err := acc.Delete(ctx, id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, _, ok := fake.Credential("codex-a.json"); ok {
		t.Error("engine credential not deleted")
	}
}

func TestAccountsAdapterLogin(t *testing.T) {
	ctx := context.Background()
	acc, fake, _ := newAccountsAdapter(t)
	l, err := acc.StartLogin(ctx, "codex")
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	if l.State == "" || !strings.HasPrefix(l.URL, "https://") || l.UserCode != "" {
		t.Errorf("login = %+v", l)
	}
	if st, err := acc.LoginStatus(ctx, l.State); err != nil || st.Status != "wait" {
		t.Errorf("LoginStatus = %+v, %v", st, err)
	}
	redirect := "http://localhost:1455/auth/callback?code=c&state=" + l.State
	if err := acc.CompleteLogin(ctx, l.State, redirect); err != nil {
		t.Fatalf("CompleteLogin: %v", err)
	}
	if state, u := fake.LastCallback(); state != l.State || u != redirect {
		t.Errorf("engine callback = (%q, %q)", state, u)
	}
	fake.FinishLogin(l.State, cliproxyapi.Credential{Name: "codex-new.json", Provider: "codex", Email: "n@example.com"})
	if st, err := acc.LoginStatus(ctx, l.State); err != nil || st.Status != "ok" {
		t.Errorf("LoginStatus = %+v, %v", st, err)
	}

	d, err := acc.StartLogin(ctx, "grok")
	if err != nil || d.UserCode == "" {
		t.Errorf("device login = %+v, %v", d, err)
	}
	if err := acc.CancelLogin(ctx, d.State); err != nil {
		t.Errorf("CancelLogin: %v", err)
	}
	if err := acc.CancelLogin(ctx, d.State); !errors.Is(err, accounts.ErrLoginNotFound) {
		t.Errorf("second CancelLogin err = %v, want ErrLoginNotFound", err)
	}
}

func TestAccountsAdapterImport(t *testing.T) {
	ctx := context.Background()
	acc, fake, _ := newAccountsAdapter(t)
	a, err := acc.Import(ctx, "antigravity", []byte(`{"email":"i@example.com","type":"antigravity"}`))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if a.ID == "" || a.Provider != "antigravity" || a.Email != "i@example.com" || !a.Enabled {
		t.Errorf("imported = %+v", a)
	}
	if _, err := acc.Import(ctx, "antigravity", []byte(`{"type":"codex"}`)); !errors.Is(err, accounts.ErrBadCredential) {
		t.Errorf("mismatched Import err = %v, want ErrBadCredential", err)
	}
	if n := fake.Calls("/credentials"); n != 2 { // one upload, one list after it
		t.Errorf("credentials calls = %d, want 2", n)
	}
}

// TestAccountsOverHTTPNeverNameEngine drives the accounts operations over the real service and checks the error
// answers: the right status, and no engine name.
func TestAccountsOverHTTPNeverNameEngine(t *testing.T) {
	acc, _, _ := newAccountsAdapter(t)
	srv := httptest.NewServer(New(Deps{Accounts: acc}).Handler())
	t.Cleanup(srv.Close)
	call := func(method, path, body string) (int, string) {
		t.Helper()
		req, err := http.NewRequestWithContext(context.Background(), method, srv.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	code, body := call(http.MethodPost, "/api/accounts/logins", `{"provider":"codex"}`)
	if code != http.StatusCreated {
		t.Fatalf("start = %d %s", code, body)
	}
	var l struct{ State string }
	if err := json.Unmarshal([]byte(body), &l); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		method, path, body string
		want               int
	}{
		{http.MethodPost, "/api/accounts/logins/" + l.State + "/complete", `{"redirect_url":"http://localhost:1455/cb?code=c&state=other"}`, http.StatusBadRequest},
		{http.MethodPost, "/api/accounts/logins", `{"provider":"nope"}`, http.StatusBadRequest},
		{http.MethodGet, "/api/accounts?provider=nope", "", http.StatusBadRequest},
		{http.MethodGet, "/api/accounts/logins/unknown", "", http.StatusNotFound},
		{http.MethodPost, "/api/accounts/import", `{"provider":"codex","credential":{"type":"antigravity"}}`, http.StatusBadRequest},
		{http.MethodDelete, "/api/accounts/42", "", http.StatusNotFound},
	} {
		code, body := call(c.method, c.path, c.body)
		if code != c.want {
			t.Errorf("%s %s = %d %s, want %d", c.method, c.path, code, body, c.want)
		}
		if lower := strings.ToLower(body); strings.Contains(lower, "cliproxy") || strings.Contains(lower, "bifrost") {
			t.Errorf("%s %s answer names an engine: %s", c.method, c.path, body)
		}
	}
}

func TestAliasesAdapterCooling(t *testing.T) {
	until := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	snap := []cooling.Cooling{{Target: alias.Target{Provider: "up", Model: "m1"}, Until: until, Reason: "402"}}
	got, err := NewAliases(nil, nil, func() []cooling.Cooling { return snap }).Cooling(context.Background())
	if err != nil || len(got) != 1 || got[0].Target != snap[0].Target || !got[0].Until.Equal(until) || got[0].Reason != "402" {
		t.Errorf("Cooling = %+v, %v", got, err)
	}
	if got, err := NewAliases(nil, nil, nil).Cooling(context.Background()); err != nil || len(got) != 0 {
		t.Errorf("Cooling without a cooler = %+v, %v", got, err)
	}
}

func TestNamesAdapterTranslatesSubscriptionProviders(t *testing.T) {
	names := NewNames(nil)
	ctx := context.Background()
	if got := names.Target(ctx, bifrost.OAuthProvider, "codex/gpt-x"); got != (alias.Target{Provider: "codex", Model: "gpt-x"}) {
		t.Errorf("Target(oauth) = %+v", got)
	}
	if got := names.Target(ctx, "openrouter", "vendor/m"); got != (alias.Target{Provider: "openrouter", Model: "vendor/m"}) {
		t.Errorf("Target(openrouter) = %+v", got)
	}
	cases := []struct {
		provider, model string
		ps, ms          []string
	}{
		{"", "", nil, nil},
		{"codex", "", []string{bifrost.OAuthProvider}, nil},
		{"", "codex/gpt-x", []string{bifrost.OAuthProvider}, []string{"codex/gpt-x"}},
		{"or", "", []string{"or"}, nil},
		{"", "or/vendor/m", []string{"or"}, []string{"vendor/m"}},
		{"or", "or/a", []string{"or"}, []string{"a"}},
	}
	for _, c := range cases {
		ps, ms := names.EngineFilter(ctx, c.provider, c.model)
		if !slices.Equal(ps, c.ps) || !slices.Equal(ms, c.ms) {
			t.Errorf("EngineFilter(%q, %q) = %v, %v; want %v, %v", c.provider, c.model, ps, ms, c.ps, c.ms)
		}
	}
}

type fakeLister map[string][]bifrost.KeyStatus

func (f fakeLister) RefreshProviderModels(_ context.Context, name string) ([]bifrost.KeyStatus, error) {
	return f[name], nil
}

type fakeSonioxFiles map[string]error // key → answer

func (f fakeSonioxFiles) ListFiles(_ context.Context, key string) ([]soniox.File, error) {
	return nil, f[key]
}

func TestProvidersAdapterTest(t *testing.T) {
	ctx := context.Background()
	st, rec, _ := adapterStore(t)
	rec.SonioxURL = "http://127.0.0.1:1"
	create := NewProviders(st, rec, nil, nil)
	for _, p := range []Provider{
		{Name: "up", Kind: "openai-compatible", BaseURL: "http://up.test/v1", Keys: []ProviderKey{{Name: "main", Value: "k1"}, {Name: "spare", Value: "k2"}}},
		{Name: "soniox", Kind: "soniox", Keys: []ProviderKey{{Name: "a", Value: "s1"}, {Name: "b", Value: "s2"}}},
	} {
		if _, err := create.Create(ctx, p); err != nil {
			t.Fatalf("Create %s: %v", p.Name, err)
		}
	}
	var ids []string
	if err := read(ctx, st, func(tx *store.Tx) error {
		keys, err := tx.ProviderKeys(ctx)
		for _, k := range keys {
			if k.Provider == "up" {
				ids = append(ids, k.ID)
			}
		}
		return err
	}); err != nil || len(ids) != 2 {
		t.Fatalf("keys %v %v", ids, err)
	}
	lister := fakeLister{"up": {{ID: ids[0], Status: "success"}, {ID: ids[1], Status: "list_models_failed", Description: "401 from bifrost"}}}
	ps := NewProviders(st, rec, lister, fakeSonioxFiles{"s2": errors.New("bad key")})

	r, err := ps.Test(ctx, "up")
	if err != nil || r.OK || !strings.Contains(r.Message, ": 401 from gateway") {
		t.Fatalf("one failing key = %+v, %v", r, err)
	}
	lister["up"][1].Status = "success"
	if r, err = ps.Test(ctx, "up"); err != nil || !r.OK || r.Message != "2 of 2 keys answered" {
		t.Fatalf("every key answers = %+v, %v", r, err)
	}
	if r, err = ps.Test(ctx, "soniox"); err != nil || r.OK || !strings.Contains(r.Message, "b: bad key") {
		t.Fatalf("speech provider = %+v, %v", r, err)
	}
	if _, err = ps.Test(ctx, "nope"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown provider err = %v", err)
	}
}

// TestApplyOverAdaptersIsAtomic applies remora.yaml through the real adapters: a later alias that fails validation
// leaves the provider and the alias planned before it unwritten.
func TestApplyOverAdaptersIsAtomic(t *testing.T) {
	ctx := context.Background()
	st, rec, _ := adapterStore(t)
	ps, as, ks := NewProviders(st, rec, nil, nil), NewAliases(st, rec, nil), NewKeys(st, rec)
	svc := config.NewService(ps, as, ks, rec.Batch)
	f, err := config.Parse([]byte(`
providers:
  - {name: p, kind: openai-compatible, base_url: http://up.test/v1, keys: [{name: main, value: env:P_KEY}]}
aliases:
  - {name: good, kind: chat, members: [p/m]}
  - {name: zbad, kind: chat, members: [nope]}
keys:
  - {name: k, allow: [good]}
`))
	if err != nil {
		t.Fatal(err)
	}
	sec := config.Secrets{"P_KEY": "sk-p"}
	applied, err := svc.Apply(ctx, f, sec, true)
	var ae *config.ApplyError
	if !errors.As(err, &ae) || !errors.Is(err, alias.ErrUnknownMember) || ae.Change.Name != "zbad" {
		t.Fatalf("Apply err = %v, want an *ApplyError for alias zbad", err)
	}
	if len(applied) != 0 {
		t.Errorf("applied = %+v, want none", applied)
	}
	if got, _ := ps.List(ctx); len(got) != 0 {
		t.Errorf("providers = %+v, want none", got)
	}
	if got, _ := as.List(ctx); len(got) != 0 {
		t.Errorf("aliases = %+v, want none", got)
	}

	f.Aliases = f.Aliases[:1]
	applied, err = svc.Apply(ctx, f, sec, true)
	if err != nil || len(applied) != 3 {
		t.Fatalf("Apply = %+v, %v; want 3 changes", applied, err)
	}
	if cs, err := svc.Plan(ctx, f, sec, true); err != nil || len(cs) != 0 {
		t.Errorf("Plan after apply = %+v, %v; want no changes", cs, err)
	}
}
