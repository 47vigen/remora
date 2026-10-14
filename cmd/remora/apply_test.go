package main

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/47vigen/remora/internal/config"
	"github.com/47vigen/remora/internal/reconcile"
	"github.com/47vigen/remora/internal/reconcile/reconciletest"
	"github.com/47vigen/remora/internal/store"
)

type applyHarness struct {
	st  *store.Store
	eng *reconciletest.Engine
	rec *reconcile.Reconciler
}

func newApplyHarness(t *testing.T) *applyHarness {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "remora.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	sl, err := store.NewSealer(bytes.Repeat([]byte{3}, 32))
	if err != nil {
		t.Fatalf("NewSealer: %v", err)
	}
	st.SetSealer(sl)
	eng := reconciletest.New()
	return &applyHarness{st: st, eng: eng, rec: &reconcile.Reconciler{Store: st, Engine: eng}}
}

func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

var testEnv = envOf(map[string]string{"UP_KEY": "test-upstream-key", "FULL_KEY": "sk-e2e-full"})

func parseFile(t *testing.T, src string) config.File {
	t.Helper()
	f, err := config.Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return f
}

const applyBase = `
providers:
  - name: up-a
    kind: openai-compatible
    base_url: http://up-a.test/v1
    allow_private_network: true
    keys: [{name: main, value: env:UP_KEY}]
aliases:
  - {name: fast, kind: chat, members: [up-a/m1], capabilities: [tools], context_window: 131072}
keys:
  - {name: full, value: env:FULL_KEY, allow: ["*"]}
`

// snapshot is what the store holds, secrets included.
type snapshot struct {
	Providers []store.Provider
	Keys      []store.ProviderKey
	Aliases   map[string]string // name → kind and members
	Clients   []store.ClientKey
}

func (h *applyHarness) snapshot(t *testing.T) snapshot {
	t.Helper()
	var s snapshot
	ctx := context.Background()
	err := h.st.Write(ctx, func(tx *store.Tx) error {
		var err error
		if s.Providers, err = tx.Providers(ctx); err != nil {
			return err
		}
		if s.Keys, err = tx.ProviderKeys(ctx); err != nil {
			return err
		}
		all, err := tx.Aliases(ctx)
		if err != nil {
			return err
		}
		s.Aliases = map[string]string{}
		for n, a := range all {
			var refs []string
			for _, m := range a.Members {
				refs = append(refs, m.Ref)
			}
			s.Aliases[n] = string(a.Kind) + ":" + strings.Join(refs, ",")
		}
		s.Clients, err = tx.ClientKeys(ctx)
		return err
	})
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	return s
}

func TestApplyFile(t *testing.T) {
	ctx := context.Background()
	h := newApplyHarness(t)
	var out bytes.Buffer
	if err := applyFile(ctx, h.rec, parseFile(t, applyBase), testEnv, &out); err != nil {
		t.Fatalf("applyFile: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("out = %q, want nothing (no generated key)", out.String())
	}

	s := h.snapshot(t)
	if want := []store.Provider{{
		Name: "up-a", Kind: store.KindOpenAICompatible, BaseURL: "http://up-a.test/v1", AllowPrivateNetwork: true,
	}}; !reflect.DeepEqual(s.Providers, want) {
		t.Errorf("providers = %+v, want %+v", s.Providers, want)
	}
	if len(s.Keys) != 1 || s.Keys[0].Provider != "up-a" || s.Keys[0].Name != "main" ||
		s.Keys[0].Secret != "test-upstream-key" || !s.Keys[0].Enabled {
		t.Errorf("provider keys = %+v", s.Keys)
	}
	if want := map[string]string{"fast": "chat:up-a/m1"}; !reflect.DeepEqual(s.Aliases, want) {
		t.Errorf("aliases = %v, want %v", s.Aliases, want)
	}
	k, err := h.st.ClientKeyByValue(ctx, "sk-e2e-full")
	if err != nil || k.Name != "full" || !k.Enabled || !reflect.DeepEqual(k.Allow, []string{"*"}) || k.VKID == "" {
		t.Errorf("ClientKeyByValue = (%+v, %v)", k, err)
	}
	if got := h.eng.RulesNamed(reconcile.RuleName("fast")); len(got) != 1 {
		t.Errorf("engine rules for fast = %+v, want one", got)
	}
	if _, ok := h.eng.Key("up-a", s.Keys[0].ID); !ok {
		t.Errorf("engine has no key %s under up-a", s.Keys[0].ID)
	}

	// Applying the same file again changes nothing: same ids, no engine writes.
	h.eng.Mutations()
	if err := applyFile(ctx, h.rec, parseFile(t, applyBase), testEnv, &out); err != nil {
		t.Fatalf("applyFile again: %v", err)
	}
	if got := h.eng.Mutations(); len(got) != 0 {
		t.Errorf("second apply sent %d engine writes: %+v", len(got), got)
	}
	if s2 := h.snapshot(t); !reflect.DeepEqual(s2.Keys, s.Keys) || !reflect.DeepEqual(s2.Clients, s.Clients) {
		t.Errorf("second apply changed keys: %+v → %+v", s, s2)
	}

	// Alias removed: the alias and its rule go, the provider stays.
	noAlias := strings.Replace(applyBase,
		"  - {name: fast, kind: chat, members: [up-a/m1], capabilities: [tools], context_window: 131072}\n", "", 1)
	noAlias = strings.Replace(noAlias, "aliases:\n", "aliases: []\n", 1)
	if err := applyFile(ctx, h.rec, parseFile(t, noAlias), testEnv, &out); err != nil {
		t.Fatalf("applyFile without alias: %v", err)
	}
	s = h.snapshot(t)
	if len(s.Aliases) != 0 {
		t.Errorf("aliases = %v, want none", s.Aliases)
	}
	if got := h.eng.RulesNamed(reconcile.RuleName("fast")); len(got) != 0 {
		t.Errorf("engine still has rule for fast: %+v", got)
	}
	if len(s.Providers) != 1 {
		t.Errorf("providers = %+v, want up-a kept", s.Providers)
	}
	if _, ok := h.eng.Provider("up-a"); !ok {
		t.Error("engine lost provider up-a")
	}

	// Everything removed: the store and the engine are empty.
	if err := applyFile(ctx, h.rec, config.File{}, testEnv, &out); err != nil {
		t.Fatalf("applyFile empty: %v", err)
	}
	s = h.snapshot(t)
	if len(s.Providers)+len(s.Keys)+len(s.Aliases)+len(s.Clients) != 0 {
		t.Errorf("store after empty apply = %+v", s)
	}
	if _, ok := h.eng.Provider("up-a"); ok {
		t.Error("engine still has provider up-a")
	}
	if _, err := h.st.ClientKeyByValue(ctx, "sk-e2e-full"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("ClientKeyByValue after removal err = %v, want ErrNotFound", err)
	}
}

func TestApplyFile_UpdatesInPlace(t *testing.T) {
	ctx := context.Background()
	h := newApplyHarness(t)
	var out bytes.Buffer
	if err := applyFile(ctx, h.rec, parseFile(t, applyBase), testEnv, &out); err != nil {
		t.Fatalf("applyFile: %v", err)
	}
	before := h.snapshot(t)

	changed := strings.NewReplacer(
		`allow: ["*"]`, `allow: [fast], enabled: false`,
		"env:UP_KEY", "env:UP_KEY_2",
	).Replace(applyBase)
	env := envOf(map[string]string{"UP_KEY_2": "test-upstream-key-2", "FULL_KEY": "sk-e2e-full"})
	if err := applyFile(ctx, h.rec, parseFile(t, changed), env, &out); err != nil {
		t.Fatalf("applyFile changed: %v", err)
	}
	after := h.snapshot(t)
	if after.Keys[0].ID != before.Keys[0].ID || after.Keys[0].Secret != "test-upstream-key-2" {
		t.Errorf("provider key = %+v, want id %s with the new secret", after.Keys[0], before.Keys[0].ID)
	}
	c := after.Clients[0]
	if c.ID != before.Clients[0].ID || c.Enabled || !reflect.DeepEqual(c.Allow, []string{"fast"}) || c.VKID != before.Clients[0].VKID {
		t.Errorf("client key = %+v, want same id and vk, disabled, allow [fast]", c)
	}
	if ek, _ := h.eng.Key("up-a", after.Keys[0].ID); ek.Value != "test-upstream-key-2" {
		t.Errorf("engine key value = %q", ek.Value)
	}
}

func TestApplyFile_Errors(t *testing.T) {
	tests := []struct {
		name  string
		src   string
		env   map[string]string
		wants []string // substrings of the error
	}{
		{
			name:  "literal client key",
			src:   strings.Replace(applyBase, "env:FULL_KEY", "sk-literal", 1),
			wants: []string{"keys[0].value", "env:"},
		},
		{
			name:  "literal provider key",
			src:   strings.Replace(applyBase, "env:UP_KEY", "test-upstream-key", 1),
			wants: []string{"providers[0].keys[0].value", "env:"},
		},
		{
			name:  "env var unset",
			src:   applyBase,
			env:   map[string]string{"UP_KEY": "test-upstream-key"},
			wants: []string{"keys[0].value", "FULL_KEY"},
		},
		{
			name:  "provider env var unset",
			src:   applyBase,
			env:   map[string]string{"FULL_KEY": "sk-e2e-full"},
			wants: []string{"providers[0].keys[0].value", "UP_KEY"},
		},
		{
			name:  "unknown provider kind",
			src:   strings.Replace(applyBase, "kind: openai-compatible", "kind: magic", 1),
			wants: []string{"providers[0].kind", "magic"},
		},
		{
			name:  "unknown alias kind",
			src:   strings.Replace(applyBase, "kind: chat", "kind: video", 1),
			wants: []string{"aliases[0].kind", "video"},
		},
		{
			name:  "unknown member",
			src:   strings.Replace(applyBase, "members: [up-a/m1]", "members: [nope]", 1),
			wants: []string{"nope"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			h := newApplyHarness(t)
			// Seed a different state, which a failed apply must leave as it is.
			seed := `
providers:
  - {name: up-z, kind: openai-compatible, base_url: http://up-z.test/v1, keys: [{name: main, value: env:UP_KEY}]}
aliases:
  - {name: slow, kind: chat, members: [up-z/m9]}
keys:
  - {name: old, value: env:FULL_KEY, allow: [slow]}
`
			var out bytes.Buffer
			if err := applyFile(ctx, h.rec, parseFile(t, seed), testEnv, &out); err != nil {
				t.Fatalf("seed: %v", err)
			}
			before := h.snapshot(t)
			h.eng.Mutations()

			env := testEnv
			if tt.env != nil {
				env = envOf(tt.env)
			}
			f, err := config.Parse([]byte(tt.src))
			if err == nil {
				err = applyFile(ctx, h.rec, f, env, &out)
			}
			if err == nil {
				t.Fatal("no error")
			}
			for _, w := range tt.wants {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q does not mention %q", err, w)
				}
			}
			if after := h.snapshot(t); !reflect.DeepEqual(after, before) {
				t.Errorf("store changed:\n before %+v\n after  %+v", before, after)
			}
			if got := h.eng.Mutations(); len(got) != 0 {
				t.Errorf("engine writes after a failed apply: %+v", got)
			}
		})
	}
}

func TestApplyFile_GeneratesKey(t *testing.T) {
	ctx := context.Background()
	h := newApplyHarness(t)
	src := strings.Replace(applyBase, "{name: full, value: env:FULL_KEY, allow: [\"*\"]}", "{name: full, allow: [\"*\"]}", 1)
	var out bytes.Buffer
	if err := applyFile(ctx, h.rec, parseFile(t, src), testEnv, &out); err != nil {
		t.Fatalf("applyFile: %v", err)
	}
	m := regexp.MustCompile(`^full: (sk-\S+)\n$`).FindStringSubmatch(out.String())
	if m == nil {
		t.Fatalf("out = %q, want one line \"full: sk-...\"", out.String())
	}
	if k, err := h.st.ClientKeyByValue(ctx, m[1]); err != nil || k.Name != "full" {
		t.Errorf("ClientKeyByValue(generated) = (%+v, %v)", k, err)
	}

	// A second apply keeps the generated value and prints nothing.
	out.Reset()
	if err := applyFile(ctx, h.rec, parseFile(t, src), testEnv, &out); err != nil {
		t.Fatalf("applyFile again: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("second apply printed %q", out.String())
	}
	if _, err := h.st.ClientKeyByValue(ctx, m[1]); err != nil {
		t.Errorf("generated key no longer authenticates: %v", err)
	}
}

func TestRunApply_Usage(t *testing.T) {
	for _, args := range [][]string{{"apply"}, {"apply", "-f"}, {"apply", "-f", "a.yaml", "extra"}} {
		var stdout, stderr bytes.Buffer
		if code := run(context.Background(), args, envOf(nil), &stdout, &stderr); code != 2 {
			t.Errorf("%q: code = %d, want 2 (stderr %q)", args, code, stderr.String())
		}
	}
}

// failWriter fails every write, like a closed pipe or a full disk.
type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

func TestApplyFile_GeneratedKeyNotKeptWhenOutputFails(t *testing.T) {
	ctx := context.Background()
	h := newApplyHarness(t)
	src := strings.Replace(applyBase, "{name: full, value: env:FULL_KEY, allow: [\"*\"]}", "{name: full, allow: [\"*\"]}", 1)
	if err := applyFile(ctx, h.rec, parseFile(t, src), testEnv, failWriter{}); err == nil {
		t.Fatal("applyFile with a failing output: want an error")
	}
	var keys []store.ClientKey
	if err := h.st.Write(ctx, func(tx *store.Tx) (err error) {
		keys, err = tx.ClientKeys(ctx)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for _, k := range keys {
		if k.Name == "full" {
			t.Fatalf("the unshown generated key was kept: %+v", k)
		}
	}
	// The next apply generates a new key and prints it.
	var out bytes.Buffer
	if err := applyFile(ctx, h.rec, parseFile(t, src), testEnv, &out); err != nil {
		t.Fatalf("applyFile again: %v", err)
	}
	if !regexp.MustCompile(`^full: sk-\S+\n$`).MatchString(out.String()) {
		t.Errorf("out = %q, want one generated key", out.String())
	}
}

func TestScrubWriter(t *testing.T) {
	var b bytes.Buffer
	msg := "reconcile: bifrost: POST /api/providers: status 500 (key sk-bf-abc)\n"
	n, err := scrubWriter{&b}.Write([]byte(msg))
	if err != nil || n != len(msg) {
		t.Fatalf("Write = (%d, %v)", n, err)
	}
	if got := strings.ToLower(b.String()); strings.Contains(got, "bifrost") || strings.Contains(got, "sk-bf-") {
		t.Errorf("scrubbed = %q", b.String())
	}
}

func TestApplyFile_GeneratedKeyNotStoredWhenChangeFails(t *testing.T) {
	ctx := context.Background()
	h := newApplyHarness(t)
	src := strings.Replace(applyBase, "{name: full, value: env:FULL_KEY, allow: [\"*\"]}", "{name: full, allow: [\"*\"]}", 1)
	h.eng.FailOnce("CreateProvider")
	var out bytes.Buffer
	err := applyFile(ctx, h.rec, parseFile(t, src), testEnv, &out)
	if err == nil || !strings.Contains(err.Error(), "were not stored") {
		t.Fatalf("applyFile with a failing engine: err = %v, want the not-stored note", err)
	}
	if !regexp.MustCompile(`^full: sk-\S+\n$`).MatchString(out.String()) {
		t.Errorf("out = %q, want the generated key shown before the change", out.String())
	}
	var keys []store.ClientKey
	if err := h.st.Write(ctx, func(tx *store.Tx) (err error) {
		keys, err = tx.ClientKeys(ctx)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if len(keys) != 0 {
		t.Fatalf("keys stored by a failed apply: %+v", keys)
	}
}
