package reconcile

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/47vigen/remora/internal/alias"
	"github.com/47vigen/remora/internal/cooling"
	"github.com/47vigen/remora/internal/engine/bifrost"
	"github.com/47vigen/remora/internal/engine/cliproxyapi"
	"github.com/47vigen/remora/internal/store"
)

const oauthTestKey = "sk-test-oauth-client"

func TestOAuthProviderSpec(t *testing.T) {
	ip := OAuthProvider(oauthTestKey, "http://127.0.0.1:18317")
	p := ip.Provider
	if p.Name != "remora-oauth" || p.BaseURL != "http://127.0.0.1:18317" || p.BaseType != "openai" || !p.AllowPrivateNetwork {
		t.Errorf("provider = %+v", p)
	}
	got := slices.Sorted(slices.Values(p.AllowedRequests))
	want := []string{"chat_completion", "chat_completion_stream", "list_models", "responses", "responses_stream"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("allowed = %q, want %q", got, want)
	}
	if len(ip.Keys) != 1 || ip.Keys[0].Value != oauthTestKey || !ip.Keys[0].Enabled || ip.Keys[0].ID == "" {
		t.Errorf("keys = %+v", ip.Keys)
	}
}

func TestOAuthRules(t *testing.T) {
	rules := OAuthRules()
	if len(rules) != len(cliproxyapi.Providers()) {
		t.Fatalf("len = %d, want %d", len(rules), len(cliproxyapi.Providers()))
	}
	prio := map[int]bool{}
	var grok *bifrost.RoutingRule
	for i, r := range rules {
		if r.Priority <= 0 || r.Priority >= RulePriorityBase || prio[r.Priority] {
			t.Errorf("rule %s priority %d: want unique, in 1..%d", r.Name, r.Priority, RulePriorityBase-1)
		}
		prio[r.Priority] = true
		if !strings.HasPrefix(r.Name, "remora-oauth-") || len(r.Fallbacks) != 0 {
			t.Errorf("rule = %+v", r)
		}
		if r.Name == "remora-oauth-grok" {
			grok = &rules[i]
		}
	}
	if grok == nil {
		t.Fatal("no rule for grok")
	}
	if grok.CEL != `model.startsWith("grok/")` || grok.Target != (bifrost.Route{Provider: "remora-oauth"}) {
		t.Errorf("grok rule = %+v", *grok)
	}
}

func TestToEngineTarget(t *testing.T) {
	cases := []struct{ in, want alias.Target }{
		{alias.Target{Provider: "codex", Model: "gpt-x"}, alias.Target{Provider: "remora-oauth", Model: "codex/gpt-x"}},
		{alias.Target{Provider: "grok", Model: "g-1"}, alias.Target{Provider: "remora-oauth", Model: "grok/g-1"}},
		{alias.Target{Provider: "openrouter", Model: "a/b"}, alias.Target{Provider: "openrouter", Model: "a/b"}},
	}
	for _, c := range cases {
		if got := ToEngineTarget(c.in); got != c.want {
			t.Errorf("ToEngineTarget(%+v) = %+v, want %+v", c.in, got, c.want)
		}
	}
}

// withOAuth wires the harness's reconciler the way serve does.
func (h *harness) withOAuth() {
	h.rec.Internal = append(h.rec.Internal, OAuthProvider(oauthTestKey, "http://127.0.0.1:18317"))
	h.rec.InternalRules = OAuthRules()
	h.rec.Reserved = OAuthReserved()
}

func TestReconcileRegistersOAuth(t *testing.T) {
	h := newHarness(t)
	h.base(t)
	h.withOAuth()
	h.sync(t)

	p, ok := h.eng.Provider("remora-oauth")
	if !ok || p.BaseURL != "http://127.0.0.1:18317" {
		t.Fatalf("provider remora-oauth = %+v, %v", p, ok)
	}
	if k, ok := h.eng.Key("remora-oauth", oauthKeyID); !ok || k.Value != oauthTestKey {
		t.Errorf("key = %+v, %v", k, ok)
	}
	for _, want := range OAuthRules() {
		rs := h.eng.RulesNamed(want.Name)
		if len(rs) != 1 {
			t.Fatalf("rule %s: %+v", want.Name, rs)
		}
		if rs[0].Priority != want.Priority || rs[0].CEL != want.CEL || rs[0].Target != want.Target {
			t.Errorf("rule %s = %+v, want %+v", want.Name, rs[0], want)
		}
	}
	if n := len(OAuthRules()); n != 8 {
		t.Errorf("OAuth rules = %d, want 8", n)
	}
	if rs := h.eng.RulesNamed(RuleName("fast")); len(rs) != 1 || rs[0].Priority < RulePriorityBase {
		t.Errorf("alias rule = %+v", rs)
	}

	h.eng.Mutations()
	h.sync(t)
	if got := h.eng.Mutations(); len(got) != 0 {
		t.Errorf("second Sync made calls %q", ops(got))
	}
}

// TestReconcileOAuthRulePriorityTaken: a rule remora does not want holds an OAuth rule's priority. The OAuth rule
// takes a free priority for one pass and its own on the next, after the stale rule is gone.
func TestReconcileOAuthRulePriorityTaken(t *testing.T) {
	h := newHarness(t)
	h.base(t)
	h.withOAuth()
	first := OAuthRules()[0]
	h.eng.SeedRule(bifrost.RoutingRule{Name: "hand-made", CEL: "true", Target: bifrost.Route{Provider: "up-a"}, Priority: first.Priority})
	h.sync(t)
	if rs := h.eng.RulesNamed(first.Name); len(rs) != 1 || rs[0].Priority == first.Priority {
		t.Fatalf("first pass: %s = %+v", first.Name, rs)
	}
	h.sync(t)
	if rs := h.eng.RulesNamed(first.Name); len(rs) != 1 || rs[0].Priority != first.Priority {
		t.Fatalf("second pass: %s = %+v, want priority %d", first.Name, rs, first.Priority)
	}
}

func TestAliasChainUsesEngineTarget(t *testing.T) {
	h := newHarness(t)
	h.withOAuth()
	h.write(t, putAll([]store.Provider{{Name: "openrouter", Kind: store.KindBuiltin, Type: "openrouter"}}, chat("smart", "codex/gpt-x", "openrouter/m")))
	h.sync(t)
	rs := h.eng.RulesNamed(RuleName("smart"))
	if len(rs) != 1 {
		t.Fatalf("rules = %+v", rs)
	}
	if rs[0].Target != (bifrost.Route{Provider: "remora-oauth", Model: "codex/gpt-x"}) ||
		!reflect.DeepEqual(rs[0].Fallbacks, []bifrost.Route{{Provider: "openrouter", Model: "m"}}) {
		t.Errorf("rule = %+v", rs[0])
	}

	// And as a fallback.
	h.write(t, putAll(nil, chat("smart", "openrouter/m", "grok/g-1")))
	h.sync(t)
	rs = h.eng.RulesNamed(RuleName("smart"))
	if len(rs) != 1 || rs[0].Target != (bifrost.Route{Provider: "openrouter", Model: "m"}) ||
		!reflect.DeepEqual(rs[0].Fallbacks, []bifrost.Route{{Provider: "remora-oauth", Model: "grok/g-1"}}) {
		t.Errorf("rule = %+v", rs)
	}
}

func TestReconcileRunsAlso(t *testing.T) {
	h := newHarness(t)
	h.base(t)
	calls := make(chan struct{}, 4)
	h.rec.Also = []func(context.Context) error{func(context.Context) error {
		calls <- struct{}{}
		return nil
	}}
	if err := h.rec.SyncAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 {
		t.Errorf("Also ran %d times, want 1", len(calls))
	}
}

func TestReconcileFiltersCooling(t *testing.T) {
	h := newHarness(t)
	h.withOAuth()
	or := store.Provider{Name: "openrouter", Kind: store.KindBuiltin, Type: "openrouter"}
	h.write(t, putAll([]store.Provider{or}, chat("pair", "openrouter/a", "openrouter/b"), chat("sub", "codex/gpt-x", "openrouter/b")))

	ctx := context.Background()
	cl := cooling.New(cooling.Config{}, h.rec.Sync, nil, nil)
	h.rec.Filter = cl.Filter
	h.sync(t)
	if rs := h.eng.RulesNamed(RuleName("pair")); len(rs) != 1 || rs[0].Target != (bifrost.Route{Provider: "openrouter", Model: "a"}) {
		t.Fatalf("before cooling: %+v", rs)
	}

	// A cooling member leaves the chain; Observe runs apply, which is Sync.
	if err := cl.Observe(ctx, cooling.Event{At: time.Now(), Target: alias.Target{Provider: "openrouter", Model: "a"}, StatusCode: 402}); err != nil {
		t.Fatal(err)
	}
	rs := h.eng.RulesNamed(RuleName("pair"))
	if len(rs) != 1 || rs[0].Target != (bifrost.Route{Provider: "openrouter", Model: "b"}) || len(rs[0].Fallbacks) != 0 {
		t.Fatalf("pair while a cools = %+v", rs)
	}

	// The filter sees user names: a cooling codex member is dropped before it is mapped to the OAuth engine.
	if err := cl.Observe(ctx, cooling.Event{At: time.Now(), Target: alias.Target{Provider: "codex", Model: "gpt-x"}, StatusCode: 402}); err != nil {
		t.Fatal(err)
	}
	rs = h.eng.RulesNamed(RuleName("sub"))
	if len(rs) != 1 || rs[0].Target != (bifrost.Route{Provider: "openrouter", Model: "b"}) || len(rs[0].Fallbacks) != 0 {
		t.Fatalf("sub while codex cools = %+v", rs)
	}
}
