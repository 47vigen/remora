package api

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/47vigen/remora/internal/alias"
	"github.com/47vigen/remora/internal/reconcile"
	"github.com/47vigen/remora/internal/reconcile/reconciletest"
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
	ps := NewProviders(st, rec)

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
	if _, err := NewProviders(st, rec).Create(ctx, Provider{
		Name: "up", Kind: "openai-compatible",
		BaseURL: "http://up.test/v1", Keys: []ProviderKey{{Name: "main", Value: "k1"}},
	}); err != nil {
		t.Fatalf("Create provider: %v", err)
	}
	as := NewAliases(st, rec)
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

	k, value, err := ks.Create(ctx, "svc", []string{"fast"}, "")
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
