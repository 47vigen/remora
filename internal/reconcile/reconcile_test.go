package reconcile

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/47vigen/remora/internal/alias"
	"github.com/47vigen/remora/internal/engine/bifrost"
	"github.com/47vigen/remora/internal/store"
)

// syncBuffer is a bytes.Buffer safe for a logger and a test reading it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type harness struct {
	st  *store.Store
	eng *fakeEngine
	rec *Reconciler
	log *syncBuffer
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "remora.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	sl, err := store.NewSealer(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatalf("NewSealer: %v", err)
	}
	st.SetSealer(sl)
	h := &harness{st: st, eng: newFakeEngine(), log: &syncBuffer{}}
	h.rec = &Reconciler{Store: st, Engine: h.eng, Log: slog.New(slog.NewTextHandler(h.log, nil))}
	return h
}

// write changes the store directly, without reconciling.
func (h *harness) write(t *testing.T, fn func(ctx context.Context, tx *store.Tx) error) {
	t.Helper()
	ctx := context.Background()
	if err := h.st.Write(ctx, func(tx *store.Tx) error { return fn(ctx, tx) }); err != nil {
		t.Fatalf("store.Write: %v", err)
	}
}

func (h *harness) sync(t *testing.T) {
	t.Helper()
	if err := h.rec.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}
}

func custom(name string) store.Provider {
	return store.Provider{Name: name, Kind: store.KindOpenAICompatible, BaseURL: "http://" + name + ".test/v1"}
}

func members(refs ...string) []alias.Member {
	out := make([]alias.Member, 0, len(refs))
	for _, r := range refs {
		out = append(out, alias.Member{Ref: r})
	}
	return out
}

func chat(name string, refs ...string) alias.Alias {
	return alias.Alias{Name: name, Kind: alias.KindChat, Members: members(refs...)}
}

// putAll returns a store change that writes providers (each with one key "<name>-key"), and aliases.
func putAll(providers []store.Provider, aliases ...alias.Alias) func(ctx context.Context, tx *store.Tx) error {
	return func(ctx context.Context, tx *store.Tx) error {
		for _, p := range providers {
			if err := tx.PutProvider(ctx, p); err != nil {
				return err
			}
			if err := tx.PutProviderKey(ctx, store.ProviderKey{
				ID: p.Name + "-key", Provider: p.Name, Name: "main", Secret: "test-secret-" + p.Name, Enabled: true,
			}); err != nil {
				return err
			}
		}
		for _, a := range aliases {
			if err := tx.PutAlias(ctx, a); err != nil {
				return err
			}
		}
		return nil
	}
}

const c1Value = "sk-test-client-one"

// base seeds up-a and up-b (one key each), client key c1 allowed "*", and alias fast [up-a/m1, up-b/m1]. It
// returns c1's id.
func (h *harness) base(t *testing.T) string {
	t.Helper()
	var id string
	h.write(t, func(ctx context.Context, tx *store.Tx) error {
		if err := putAll([]store.Provider{custom("up-a"), custom("up-b")}, chat("fast", "up-a/m1", "up-b/m1"))(ctx, tx); err != nil {
			return err
		}
		k, err := tx.PutClientKey(ctx, store.ClientKey{Name: "c1", Allow: []string{"*"}, Enabled: true}, c1Value)
		id = k.ID
		return err
	})
	return id
}

func ops(calls []call) []string {
	out := make([]string, 0, len(calls))
	for _, c := range calls {
		out = append(out, c.Op+" "+c.Name)
	}
	return out
}

func TestSync_CreatesEverything(t *testing.T) {
	h := newHarness(t)
	c1 := h.base(t)
	h.sync(t)
	ctx := context.Background()

	for _, name := range []string{"up-a", "up-b"} {
		p, ok := h.eng.provider(name)
		if !ok {
			t.Fatalf("provider %s missing", name)
		}
		if p.BaseType != "openai" || p.BaseURL != "http://"+name+".test/v1" {
			t.Errorf("provider %s = %+v", name, p)
		}
	}
	k, ok := h.eng.key("up-a", "up-a-key")
	if !ok {
		t.Fatal("key up-a-key missing under up-a")
	}
	if k.Name != "up-a-key" || k.Value != "test-secret-up-a" || !k.Enabled {
		t.Errorf("key = %+v, want name = id, the secret, enabled", k)
	}

	vk, ok := h.eng.vk(VirtualKeyName(c1))
	if !ok || !vk.Active {
		t.Fatalf("virtual key %s = %+v, %v", VirtualKeyName(c1), vk, ok)
	}
	ck, err := h.st.ClientKeyByValue(ctx, c1Value)
	if err != nil {
		t.Fatal(err)
	}
	if ck.VKID != vk.ID || ck.VKValue != vk.Value {
		t.Errorf("stored VK = %q %q, engine %q %q", ck.VKID, ck.VKValue, vk.ID, vk.Value)
	}

	rules := h.eng.rulesNamed("remora:alias:fast")
	if len(rules) != 1 {
		t.Fatalf("rules = %+v", rules)
	}
	r := rules[0]
	if r.CEL != RuleCEL("fast") || r.Target != (bifrost.Route{Provider: "up-a", Model: "m1"}) ||
		!reflect.DeepEqual(r.Fallbacks, []bifrost.Route{{Provider: "up-b", Model: "m1"}}) {
		t.Errorf("rule = %+v", r)
	}
	ids, err := h.st.AliasRuleIDs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if ids["fast"] != r.ID {
		t.Errorf("AliasRuleIDs[fast] = %q, want %q", ids["fast"], r.ID)
	}

	// Order: providers, then keys, then virtual keys, then rules.
	got := ops(h.eng.mutations())
	want := []string{
		"CreateProvider up-a", "CreateProvider up-b", "CreateKey up-a-key", "CreateKey up-b-key",
		"CreateVirtualKey " + VirtualKeyName(c1), "CreateRoutingRule remora:alias:fast",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("calls = %q, want %q", got, want)
	}
}

func TestSync_Idempotent(t *testing.T) {
	h := newHarness(t)
	h.base(t)
	h.sync(t)
	h.eng.mutations()
	h.sync(t)
	if got := h.eng.mutations(); len(got) != 0 {
		t.Errorf("second Sync made calls %q", ops(got))
	}
}

func TestSync_StoreWins(t *testing.T) {
	h := newHarness(t)
	h.base(t)
	h.eng.seedProvider(bifrost.Provider{Name: "stray", BaseType: "openai", BaseURL: "http://stray.test"},
		bifrost.Key{ID: "stray-key", Name: "stray-key", Value: "x", Enabled: true})
	h.eng.seedProvider(bifrost.Provider{Name: "up-a", BaseType: "openai"},
		bifrost.Key{ID: "manual-key", Name: "manual-key", Value: "x", Enabled: true})
	h.eng.seedVirtualKey("manual")
	h.eng.seedRule(bifrost.RoutingRule{Name: "hand-made", CEL: "true", Target: bifrost.Route{Provider: "up-a"}})
	h.rec.Internal = []InternalProvider{{
		Provider: bifrost.Provider{Name: "internal-x", BaseType: "openai", BaseURL: "http://internal.test"},
		Keys:     []bifrost.Key{{ID: "internal-x-key", Name: "internal-x-key", Value: "internal-secret", Enabled: true}},
	}}
	h.sync(t)

	if _, ok := h.eng.provider("stray"); ok {
		t.Error("provider stray kept")
	}
	if _, ok := h.eng.key("up-a", "manual-key"); ok {
		t.Error("unknown key manual-key under up-a kept")
	}
	if _, ok := h.eng.vk("manual"); ok {
		t.Error("virtual key manual kept")
	}
	if r := h.eng.rulesNamed("hand-made"); len(r) != 0 {
		t.Error("rule hand-made kept")
	}
	if _, ok := h.eng.provider("internal-x"); !ok {
		t.Error("internal provider missing")
	}
	if k, ok := h.eng.key("internal-x", "internal-x-key"); !ok || k.Value != "internal-secret" {
		t.Errorf("internal key = %+v, %v", k, ok)
	}
	// up-a existed in the engine and was not created again; it was updated to the store's state.
	if p, _ := h.eng.provider("up-a"); p.BaseURL != "http://up-a.test/v1" {
		t.Errorf("up-a = %+v", p)
	}
}

func TestSync_KeepsOAuthObjects(t *testing.T) {
	h := newHarness(t)
	h.base(t)
	h.eng.seedProvider(bifrost.Provider{Name: bifrost.OAuthProvider, BaseType: "openai", BaseURL: "http://oauth.test"})
	h.eng.seedRule(bifrost.RoutingRule{Name: "remora-oauth-codex", CEL: "true", Target: bifrost.Route{Provider: bifrost.OAuthProvider}, Priority: 1})
	h.eng.seedRule(bifrost.RoutingRule{Name: "remora-oauth-grok", CEL: "true", Target: bifrost.Route{Provider: bifrost.OAuthProvider}, Priority: 2})
	h.rec.InternalRules = []bifrost.RoutingRule{{
		Name: "remora-oauth-x", CEL: `model.startsWith("x/")`, Target: bifrost.Route{Provider: bifrost.OAuthProvider},
	}}
	h.sync(t)

	for _, c := range h.eng.mutations() {
		if strings.HasPrefix(c.Op, "Delete") {
			t.Errorf("unexpected %s %s", c.Op, c.Name)
		}
	}
	if _, ok := h.eng.provider(bifrost.OAuthProvider); !ok {
		t.Error("provider remora-oauth deleted")
	}
	for _, name := range []string{"remora-oauth-codex", "remora-oauth-grok", "remora-oauth-x"} {
		if len(h.eng.rulesNamed(name)) != 1 {
			t.Errorf("rule %s: %+v", name, h.eng.rulesNamed(name))
		}
	}
	h.sync(t)
	if got := h.eng.mutations(); len(got) != 0 {
		t.Errorf("second Sync made calls %q", ops(got))
	}
	if len(h.eng.rulesNamed("remora-oauth-x")) != 1 {
		t.Error("internal rule lost on second Sync")
	}
}

func TestSync_EngineWiped(t *testing.T) {
	h := newHarness(t)
	h.base(t)
	h.sync(t)
	ctx := context.Background()
	before, _ := h.st.ClientKeyByValue(ctx, c1Value)

	h.eng.reset()
	h.sync(t)
	after, err := h.st.ClientKeyByValue(ctx, c1Value)
	if err != nil {
		t.Fatal(err)
	}
	vk, ok := h.eng.vk(VirtualKeyName(after.ID))
	if !ok {
		t.Fatal("virtual key not recreated")
	}
	if after.VKID != vk.ID || after.VKValue != vk.Value || after.VKValue == before.VKValue {
		t.Errorf("stored VK %q %q, engine %q %q, before %q", after.VKID, after.VKValue, vk.ID, vk.Value, before.VKValue)
	}
	rules := h.eng.rulesNamed("remora:alias:fast")
	if len(rules) != 1 {
		t.Fatalf("rules = %+v", rules)
	}
	ids, _ := h.st.AliasRuleIDs(ctx)
	if ids["fast"] != rules[0].ID {
		t.Errorf("rule id %q, engine %q", ids["fast"], rules[0].ID)
	}
	for _, name := range []string{"up-a", "up-b"} {
		if _, ok := h.eng.provider(name); !ok {
			t.Errorf("provider %s not recreated", name)
		}
	}
	if _, ok := h.eng.key("up-b", "up-b-key"); !ok {
		t.Error("key not recreated")
	}
}

func TestSync_AliasChange(t *testing.T) {
	h := newHarness(t)
	h.base(t)
	h.sync(t)
	h.eng.mutations()
	h.write(t, putAll(nil, chat("fast", "up-b/m1", "up-a/m1")))
	h.sync(t)

	got := h.eng.mutations()
	if len(got) != 1 || got[0].Op != "UpdateRoutingRule" {
		t.Fatalf("calls = %q", ops(got))
	}
	r := got[0].Arg.(bifrost.RoutingRule)
	if r.Target != (bifrost.Route{Provider: "up-b", Model: "m1"}) ||
		!reflect.DeepEqual(r.Fallbacks, []bifrost.Route{{Provider: "up-a", Model: "m1"}}) {
		t.Errorf("rule = %+v", r)
	}
}

// TestChange_AliasRecreatedKeepsRuleID recreates an alias inside one Change, which clears its stored rule id in the
// transaction. The pass must compare against the transaction's view, or the commit loses the id the engine holds.
func TestChange_AliasRecreatedKeepsRuleID(t *testing.T) {
	h := newHarness(t)
	h.base(t)
	h.sync(t)
	ctx := context.Background()
	before, err := h.st.AliasRuleIDs(ctx)
	if err != nil || before["fast"] == "" {
		t.Fatalf("AliasRuleIDs = (%v, %v)", before, err)
	}
	if err := h.rec.Change(ctx, func(ctx context.Context, tx *store.Tx) error {
		if err := tx.DeleteAlias(ctx, "fast"); err != nil {
			return err
		}
		return tx.PutAlias(ctx, chat("fast", "up-a/m1", "up-b/m1"))
	}); err != nil {
		t.Fatalf("Change: %v", err)
	}
	after, err := h.st.AliasRuleIDs(ctx)
	if err != nil || after["fast"] != before["fast"] {
		t.Errorf("AliasRuleIDs after recreate = (%v, %v), want fast: %q", after, err, before["fast"])
	}
}

func TestSync_NestedAliasFlattened(t *testing.T) {
	h := newHarness(t)
	h.write(t, putAll([]store.Provider{custom("up-a"), custom("up-c")},
		chat("inner", "up-a/m1"), chat("memory/fast", "inner", "up-c/m1")))
	h.sync(t)
	rules := h.eng.rulesNamed(RuleName("memory/fast"))
	if len(rules) != 1 {
		t.Fatalf("rules = %+v", rules)
	}
	r := rules[0]
	if r.CEL != RuleCEL("memory/fast") || r.Target != (bifrost.Route{Provider: "up-a", Model: "m1"}) ||
		!reflect.DeepEqual(r.Fallbacks, []bifrost.Route{{Provider: "up-c", Model: "m1"}}) {
		t.Errorf("rule = %+v", r)
	}
}

func TestSync_BadAliasSkipped(t *testing.T) {
	h := newHarness(t)
	h.base(t)
	h.write(t, putAll(nil, chat("x", "nope")))
	h.sync(t)
	if len(h.eng.rulesNamed(RuleName("fast"))) != 1 {
		t.Error("rule for fast missing")
	}
	if len(h.eng.rulesNamed(RuleName("x"))) != 0 {
		t.Error("rule for x pushed")
	}
	if !strings.Contains(h.log.String(), "level=ERROR") || !strings.Contains(h.log.String(), "alias=x") {
		t.Errorf("log = %q", h.log.String())
	}

	// A rule pushed while the alias was valid is removed once it breaks.
	h.write(t, putAll(nil, chat("x", "up-a/m2")))
	h.sync(t)
	if len(h.eng.rulesNamed(RuleName("x"))) != 1 {
		t.Fatal("rule for x missing once valid")
	}
	h.write(t, putAll(nil, chat("x", "nope")))
	h.sync(t)
	if len(h.eng.rulesNamed(RuleName("x"))) != 0 {
		t.Error("rule for broken alias x kept")
	}
	ids, _ := h.st.AliasRuleIDs(context.Background())
	if _, ok := ids["x"]; ok {
		t.Errorf("rule id of broken alias kept: %v", ids)
	}
}

func TestSync_DisabledKeyDeactivatesVK(t *testing.T) {
	h := newHarness(t)
	c1 := h.base(t)
	h.sync(t)
	h.eng.mutations()
	h.write(t, func(ctx context.Context, tx *store.Tx) error {
		_, err := tx.PutClientKey(ctx, store.ClientKey{ID: c1, Name: "c1", Allow: []string{"*"}, Enabled: false}, "")
		return err
	})
	h.sync(t)
	got := h.eng.mutations()
	if len(got) != 1 || got[0].Op != "UpdateVirtualKey" || got[0].Arg.(bifrost.VirtualKey).Active {
		t.Fatalf("calls = %+v", got)
	}
	if vk, _ := h.eng.vk(VirtualKeyName(c1)); vk.Active {
		t.Error("virtual key still active")
	}
}

func TestSync_KeyUpdateSendsSecret(t *testing.T) {
	h := newHarness(t)
	h.base(t)
	h.sync(t)
	h.eng.mutations()
	h.write(t, func(ctx context.Context, tx *store.Tx) error {
		return tx.PutProviderKey(ctx, store.ProviderKey{ID: "up-a-key", Provider: "up-a", Name: "main", Secret: "test-secret-up-a", Enabled: false})
	})
	h.sync(t)
	got := h.eng.mutations()
	if len(got) != 1 || got[0].Op != "UpdateKey" {
		t.Fatalf("calls = %q", ops(got))
	}
	if k := got[0].Arg.(bifrost.Key); k.Value != "test-secret-up-a" || k.Enabled {
		t.Errorf("UpdateKey sent %+v", k)
	}
}

func TestSync_RulePrioritiesUniqueAndStable(t *testing.T) {
	h := newHarness(t)
	h.base(t)
	// A rule remora does not own holds the first free priority; it is deleted only after the pushes.
	h.eng.seedRule(bifrost.RoutingRule{Name: "hand-made", CEL: "true", Target: bifrost.Route{Provider: "up-a"}, Priority: RulePriorityBase})
	h.write(t, putAll(nil, chat("b", "up-a/m1", "up-b/m1"), chat("d", "up-b/m1")))
	h.sync(t)

	prio := func() map[string]int {
		out := map[string]int{}
		for _, name := range []string{"b", "d", "fast", "a", "c"} {
			if rs := h.eng.rulesNamed(RuleName(name)); len(rs) == 1 {
				out[name] = rs[0].Priority
			}
		}
		return out
	}
	first := prio()
	seen := map[int]bool{}
	for name, p := range first {
		if p < RulePriorityBase || seen[p] {
			t.Errorf("alias %s has priority %d (all: %v)", name, p, first)
		}
		seen[p] = true
	}
	if len(first) != 3 {
		t.Fatalf("priorities = %v", first)
	}

	// New aliases sorting before and between existing ones leave the existing priorities alone.
	h.eng.mutations()
	h.write(t, putAll(nil, chat("a", "up-a/m1"), chat("c", "up-b/m1")))
	h.sync(t)
	second := prio()
	for name, p := range first {
		if second[name] != p {
			t.Errorf("alias %s moved from priority %d to %d", name, p, second[name])
		}
	}
	for _, c := range h.eng.mutations() {
		if c.Op != "CreateRoutingRule" {
			t.Errorf("unexpected %s %s", c.Op, c.Name)
		}
	}
}

func TestSync_DedupesRuleNames(t *testing.T) {
	h := newHarness(t)
	h.base(t)
	h.sync(t)
	keep := h.eng.rulesNamed(RuleName("fast"))[0]
	h.eng.seedRule(bifrost.RoutingRule{Name: RuleName("fast"), CEL: RuleCEL("fast"), Target: bifrost.Route{Provider: "up-a", Model: "m1"}, Priority: 5})
	h.sync(t)
	rules := h.eng.rulesNamed(RuleName("fast"))
	if len(rules) != 1 || rules[0].ID != keep.ID {
		t.Errorf("rules = %+v, want only %s", rules, keep.ID)
	}
}

func TestSync_KeyMovedBetweenProviders(t *testing.T) {
	h := newHarness(t)
	h.base(t)
	h.sync(t)
	h.write(t, func(ctx context.Context, tx *store.Tx) error {
		return tx.PutProviderKey(ctx, store.ProviderKey{ID: "up-a-key", Provider: "up-b", Name: "moved", Secret: "test-secret-moved", Enabled: true})
	})
	h.sync(t)
	if _, ok := h.eng.key("up-a", "up-a-key"); ok {
		t.Error("key still under up-a")
	}
	if k, ok := h.eng.key("up-b", "up-a-key"); !ok || k.Value != "test-secret-moved" {
		t.Errorf("key under up-b = %+v, %v", k, ok)
	}
}

func TestChange_RecoveryRepushesTouched(t *testing.T) {
	h := newHarness(t)
	h.base(t)
	h.sync(t)
	h.eng.mutations()
	h.eng.failOnce("CreateRoutingRule")

	err := h.rec.Change(context.Background(), func(ctx context.Context, tx *store.Tx) error {
		p := custom("up-a")
		p.BaseURL = "http://up-a.test/v2"
		if err := tx.PutProvider(ctx, p); err != nil {
			return err
		}
		return tx.PutAlias(ctx, chat("slow", "up-a/m2"))
	})
	if err == nil {
		t.Fatal("Change succeeded")
	}
	var updates []bifrost.Provider
	for _, c := range h.eng.mutations() {
		if c.Op == "UpdateProvider" && c.Name == "up-a" {
			updates = append(updates, c.Arg.(bifrost.Provider))
		}
	}
	if len(updates) != 2 || updates[0].BaseURL != "http://up-a.test/v2" || updates[1].BaseURL != "http://up-a.test/v1" {
		t.Fatalf("up-a updates = %+v, want the change then the committed state", updates)
	}
	if p, _ := h.eng.provider("up-a"); p.BaseURL != "http://up-a.test/v1" {
		t.Errorf("engine up-a = %+v", p)
	}
	h.sync(t)
	if got := h.eng.mutations(); len(got) != 0 {
		t.Errorf("Sync after recovery made calls %q", ops(got))
	}
}

func TestChange_RollsBackOnEngineError(t *testing.T) {
	h := newHarness(t)
	h.base(t)
	h.sync(t)
	h.eng.mutations()
	h.eng.failOnce("CreateRoutingRule")

	err := h.rec.Change(context.Background(), func(ctx context.Context, tx *store.Tx) error {
		return tx.PutAlias(ctx, chat("slow", "up-a/m2"))
	})
	if err == nil {
		t.Fatal("Change succeeded")
	}
	aliases, _ := h.st.Aliases(context.Background())
	if _, ok := aliases["slow"]; ok {
		t.Error("alias slow committed")
	}
	if len(h.eng.rulesNamed(RuleName("slow"))) != 0 {
		t.Error("rule for slow exists")
	}
	got := h.eng.mutations()
	if len(got) != 1 || got[0].Op != "CreateRoutingRule" {
		t.Errorf("calls = %q, want only the failed create", ops(got))
	}
}

func TestChange_Commits(t *testing.T) {
	h := newHarness(t)
	h.base(t)
	h.sync(t)
	err := h.rec.Change(context.Background(), putAll(nil, chat("slow", "up-a/m2")))
	if err != nil {
		t.Fatal(err)
	}
	rules := h.eng.rulesNamed(RuleName("slow"))
	if len(rules) != 1 {
		t.Fatalf("rules = %+v", rules)
	}
	ids, _ := h.st.AliasRuleIDs(context.Background())
	if ids["slow"] != rules[0].ID {
		t.Errorf("rule id %q, engine %q", ids["slow"], rules[0].ID)
	}
}

func TestChange_Invalid(t *testing.T) {
	builtin := func(name, typ string) store.Provider {
		return store.Provider{Name: name, Kind: store.KindBuiltin, Type: typ}
	}
	cases := []struct {
		name     string
		reserved []string
		change   func(ctx context.Context, tx *store.Tx) error
		wantErr  error // nil: accepted
	}{
		{"custom named like a standard provider", nil, putAll([]store.Provider{custom("openai")}), ErrInvalid},
		{"builtin with an unknown type", nil, putAll([]store.Provider{builtin("up-z", "up-z")}), ErrInvalid},
		{"builtin named unlike its type", nil, putAll([]store.Provider{builtin("or", "openrouter")}), ErrInvalid},
		{"custom without base URL", nil, putAll([]store.Provider{{Name: "up-n", Kind: store.KindOpenAICompatible}}), ErrInvalid},
		{"soniox before plan 04", nil, putAll([]store.Provider{{Name: "soniox", Kind: store.KindSoniox, BaseURL: "http://x.test"}}), ErrInvalid},
		{"alias prefix is a provider", nil, putAll(nil, chat("up-a/x", "up-a/m1")), ErrInvalid},
		{"alias prefix is a standard provider", nil, putAll(nil, chat("openai/x", "up-a/m1")), ErrInvalid},
		{"cycle", nil, putAll(nil, chat("p", "q"), chat("q", "p")), alias.ErrCycle},
		{"unknown member provider", nil, putAll(nil, chat("y", "nobody/m1")), ErrInvalid},
		{"store provider remora-oauth", nil, putAll([]store.Provider{custom("remora-oauth")}), ErrInvalid},
		{"store provider remora-x", nil, putAll([]store.Provider{custom("remora-x")}), ErrInvalid},
		{"reserved provider name", []string{"codex", "grok"}, putAll([]store.Provider{custom("codex")}), ErrInvalid},
		{"alias prefix is reserved", []string{"codex", "grok"}, putAll(nil, chat("grok/fast", "up-a/m1")), ErrInvalid},
		{"fallback provider without key", nil, func(ctx context.Context, tx *store.Tx) error {
			if err := tx.PutProvider(ctx, custom("up-k")); err != nil {
				return err
			}
			return tx.PutAlias(ctx, chat("y", "up-a/m1", "up-k/m1"))
		}, ErrInvalid},
		{"engine reference as key value", nil, func(ctx context.Context, tx *store.Tx) error {
			return tx.PutProviderKey(ctx, store.ProviderKey{ID: "up-a-key", Provider: "up-a", Name: "main", Secret: "env.SECRET", Enabled: true})
		}, ErrInvalid},
		{"reserved provider as member", []string{"codex", "grok"}, putAll(nil, chat("y", "codex/gpt-x")), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.base(t)
			h.rec.Reserved = tc.reserved
			h.sync(t)
			h.eng.mutations()
			lists := h.eng.lists

			err := h.rec.Change(context.Background(), tc.change)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("Change: %v", err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) || !errors.Is(err, ErrInvalid) {
				t.Fatalf("Change = %v, want %v and ErrInvalid", err, tc.wantErr)
			}
			if got := h.eng.mutations(); len(got) != 0 || h.eng.lists != lists {
				t.Errorf("engine calls %q, lists %d → %d", ops(got), lists, h.eng.lists)
			}
			aliases, _ := h.st.Aliases(context.Background())
			if len(aliases) != 1 {
				t.Errorf("store changed: aliases %v", aliases)
			}
		})
	}
}

func TestSync_InvalidProviderFails(t *testing.T) {
	h := newHarness(t)
	h.write(t, putAll([]store.Provider{custom("openai")}))
	if err := h.rec.Sync(context.Background()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Sync = %v, want ErrInvalid", err)
	}
	if got := h.eng.mutations(); len(got) != 0 {
		t.Errorf("calls %q", ops(got))
	}
}

func TestRun(t *testing.T) {
	h := newHarness(t)
	h.base(t)
	h.rec.Interval = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		h.rec.Run(ctx)
		close(done)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for len(h.eng.rulesNamed(RuleName("fast"))) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	h.eng.reset() // the timer pass recreates it
	for len(h.eng.rulesNamed(RuleName("fast"))) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
	if len(h.eng.rulesNamed(RuleName("fast"))) != 1 {
		t.Error("Run did not reconcile")
	}
}

func TestNames(t *testing.T) {
	if got, want := RuleCEL(`memory/fast`), `model == "memory/fast" && provider in ["", "openai", "typesafe"]`; got != want {
		t.Errorf("RuleCEL = %q, want %q", got, want)
	}
	if got := RuleName("memory/fast"); got != "remora:alias:memory/fast" {
		t.Errorf("RuleName = %q", got)
	}
	if got := VirtualKeyName("id-1"); got != "remora:key:id-1" {
		t.Errorf("VirtualKeyName = %q", got)
	}
}
