package config

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/47vigen/remora/internal/alias"
)

// exportDoc exports w and parses the marshalled document, as `remora export` followed by `remora apply` does.
func exportDoc(t *testing.T, w *world) (File, []byte) {
	t.Helper()
	f, err := w.service().Export(context.Background())
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	b, err := Marshal(f)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	doc, err := Parse(b)
	if err != nil {
		t.Fatalf("Parse(Marshal(Export())): %v\n%s", err, b)
	}
	return doc, b
}

func mustParse(t *testing.T, src string) File {
	t.Helper()
	f, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v\n%s", err, src)
	}
	return f
}

func TestExportValidatesAgainstSchema(t *testing.T) {
	w := populated()
	doc, b := exportDoc(t, w)
	for _, v := range []string{"sk-or-stored-secret", "sk-up-stored-secret", "sk-up-stored-backup", "sk-client-stored"} {
		if strings.Contains(string(b), v) {
			t.Errorf("export shows the secret %q:\n%s", v, b)
		}
	}
	if err := checkSchema(b); err != nil {
		t.Errorf("schema: %v", err)
	}
	if got := doc.Providers[1].Keys; !reflect.DeepEqual(got, []FileSecretKey{
		{Name: "backup", Value: "env:REMORA_PROVIDER_UP_A_BACKUP"}, {Name: "main", Value: "env:REMORA_PROVIDER_UP_A_MAIN"},
	}) {
		t.Errorf("up-a keys = %+v", got)
	}
	for _, k := range doc.Keys {
		if k.Value != "" {
			t.Errorf("client key %s exported with value %q", k.Name, k.Value)
		}
	}
	for _, a := range doc.Aliases {
		if want := map[string]string{"inner": "round-robin"}[a.Name]; a.Strategy != want {
			t.Errorf("alias %s exported with strategy %q, want %q", a.Name, a.Strategy, want)
		}
	}
	if doc.Keys[2].Name != "off" || doc.Keys[2].Enabled == nil || *doc.Keys[2].Enabled {
		t.Errorf("disabled key exported as %+v", doc.Keys[2])
	}
}

func TestMarshalSortsByName(t *testing.T) {
	f := File{
		Providers: []FileProvider{
			{Name: "b", Kind: "soniox", Keys: []FileSecretKey{{Name: "z", Value: "env:Z"}, {Name: "a", Value: "env:A"}}},
			{Name: "a", Kind: "soniox"},
		},
		Aliases: []FileAlias{{Name: "y", Kind: "chat", Members: []string{"b/2", "a/1"}}, {Name: "x", Kind: "chat", Members: []string{"a/1"}}},
		Keys:    []FileKey{{Name: "k2"}, {Name: "k1"}},
	}
	b, err := Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Parse(b)
	if err != nil {
		t.Fatalf("%v\n%s", err, b)
	}
	if got.Version != 1 || got.Providers[0].Name != "a" || got.Providers[1].Keys[0].Name != "a" ||
		got.Aliases[0].Name != "x" || got.Keys[0].Name != "k1" {
		t.Errorf("not sorted:\n%s", b)
	}
	if !reflect.DeepEqual(got.Aliases[1].Members, []string{"b/2", "a/1"}) {
		t.Errorf("members reordered: %v", got.Aliases[1].Members)
	}
	if f.Providers[0].Name != "b" {
		t.Error("Marshal sorted its argument in place")
	}
}

func TestRoundTripNoDiff(t *testing.T) {
	w := populated()
	doc, _ := exportDoc(t, w)
	changes, err := w.service().Plan(context.Background(), doc, nil, true)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if changes == nil || len(changes) != 0 {
		t.Errorf("changes = %#v, want []Change{}", changes)
	}
}

func TestRoundTripWithSecretsNoDiff(t *testing.T) {
	w := populated()
	doc, _ := exportDoc(t, w)
	sec := Secrets{
		EnvName("openrouter", "main"): "sk-or-stored-secret",
		EnvName("up-a", "main"):       "sk-up-stored-secret",
		EnvName("up-a", "backup"):     "sk-up-stored-backup",
	}
	changes, err := w.service().Plan(context.Background(), doc, sec, true)
	if err != nil || len(changes) != 0 {
		t.Errorf("Plan = %+v, %v; want no changes", changes, err)
	}
	// Applying a document with no changes changes nothing.
	applied, err := w.service().Apply(context.Background(), doc, sec, true)
	if err != nil || len(applied) != 0 || len(w.calls()) != 0 {
		t.Errorf("Apply = %+v, %v; calls %v", applied, err, w.calls())
	}
}

func TestPlanSecretUnsetKeeps(t *testing.T) {
	w := populated()
	doc, _ := exportDoc(t, w)
	changes, err := w.service().Plan(context.Background(), doc, Secrets{"UNRELATED": "x"}, true)
	if err != nil || len(changes) != 0 {
		t.Errorf("Plan = %+v, %v; want no changes", changes, err)
	}
}

func TestPlanSecretChanged(t *testing.T) {
	w := populated()
	doc, _ := exportDoc(t, w)
	doc.Keys[0].Value = "env:FULL_KEY"
	sec := Secrets{EnvName("up-a", "main"): "sk-rotated", "FULL_KEY": "sk-client-new"}
	ctx := context.Background()
	changes, err := w.service().Plan(ctx, doc, sec, true)
	if err != nil {
		t.Fatal(err)
	}
	want := []Change{
		{Op: "update", Kind: "provider", Name: "up-a", Fields: []string{"keys"}},
		{Op: "update", Kind: "key", Name: "full", Fields: []string{"value"}},
	}
	if !reflect.DeepEqual(changes, want) {
		t.Fatalf("changes = %+v, want %+v", changes, want)
	}
	if _, err := w.service().Apply(ctx, doc, sec, true); err != nil {
		t.Fatal(err)
	}
	p := w.providers["up-a"]
	if !reflect.DeepEqual(p.Keys, []ProviderKey{{Name: "backup", Value: "sk-up-stored-backup"}, {Name: "main", Value: "sk-rotated"}}) {
		t.Errorf("up-a keys after apply = %+v", p.Keys)
	}
	k, _ := w.keyByName("full")
	if w.values[k.ID] != "sk-client-new" {
		t.Errorf("full value = %q", w.values[k.ID])
	}
	if again, _ := w.service().Plan(ctx, doc, sec, true); len(again) != 0 {
		t.Errorf("second plan = %+v", again)
	}
}

func TestPlanSecretUnsetNewProvider(t *testing.T) {
	w := populated()
	doc, _ := exportDoc(t, w)
	doc.Providers = append(doc.Providers, FileProvider{
		Name: "new", Kind: "openai-compatible", BaseURL: "http://upstream.test/new",
		Keys: []FileSecretKey{{Name: "main", Value: "env:" + EnvName("new", "main")}},
	})
	_, err := w.service().Plan(context.Background(), doc, nil, true)
	if err == nil || !strings.Contains(err.Error(), "env:REMORA_PROVIDER_NEW_MAIN") || !strings.Contains(err.Error(), `"new"`) {
		t.Fatalf("err = %v, want one naming env:REMORA_PROVIDER_NEW_MAIN and the provider", err)
	}
	if !errors.Is(err, ErrInvalid) {
		t.Errorf("err %v is not ErrInvalid", err)
	}

	// A new key on an existing provider, and a new client key, need their secrets too.
	doc, _ = exportDoc(t, w)
	doc.Providers[0].Keys = append(doc.Providers[0].Keys, FileSecretKey{Name: "extra", Value: "env:OR_EXTRA"})
	if _, err := w.service().Plan(context.Background(), doc, nil, true); err == nil || !strings.Contains(err.Error(), "env:OR_EXTRA") {
		t.Errorf("new provider key: err = %v", err)
	}
	doc, _ = exportDoc(t, w)
	doc.Keys = append(doc.Keys, FileKey{Name: "svc", Value: "env:SVC_KEY", Allow: []string{"*"}})
	if _, err := w.service().Plan(context.Background(), doc, nil, true); err == nil || !strings.Contains(err.Error(), "env:SVC_KEY") {
		t.Errorf("new client key: err = %v", err)
	}
}

func TestPlanDiff(t *testing.T) {
	tests := []struct {
		name  string
		edit  func(f *File)
		prune bool
		want  []Change
	}{
		{"added alias", func(f *File) {
			f.Aliases = append(f.Aliases, FileAlias{Name: "x", Kind: "chat", Members: []string{"fast"}})
		}, true, []Change{{Op: "create", Kind: "alias", Name: "x", Fields: []string{}}}},
		{"reordered members", func(f *File) {
			for i := range f.Aliases {
				if f.Aliases[i].Name == "fast" {
					f.Aliases[i].Members = []string{"openrouter/vendor/model-a", "inner"}
				}
			}
		}, true, []Change{{Op: "update", Kind: "alias", Name: "fast", Fields: []string{"members"}}}},
		{"several alias fields", func(f *File) {
			f.Aliases[1].Capabilities, f.Aliases[1].ContextWindow = nil, 0 // fast
		}, true, []Change{{Op: "update", Kind: "alias", Name: "fast", Fields: []string{"capabilities", "context_window"}}}},
		{"alias strategy", func(f *File) {
			f.Aliases[1].Strategy = "round-robin" // fast
		}, true, []Change{{Op: "update", Kind: "alias", Name: "fast", Fields: []string{"strategy"}}}},
		{
			"removed key with prune", func(f *File) { f.Keys = f.Keys[:2] }, true,
			[]Change{{Op: "delete", Kind: "key", Name: "off", Fields: []string{}}},
		},
		{"removed key without prune", func(f *File) { f.Keys = f.Keys[:2] }, false, []Change{}},
		{"key allow and enabled", func(f *File) {
			f.Keys[1].Allow = []string{"fast"}
			f.Keys[2].Enabled = nil
		}, true, []Change{
			{Op: "update", Kind: "key", Name: "limited", Fields: []string{"allow"}},
			{Op: "update", Kind: "key", Name: "off", Fields: []string{"enabled"}},
		}},
		{"provider fields", func(f *File) {
			f.Providers[1].BaseURL = "http://upstream.test/other"
			f.Providers[1].AllowPrivateNetwork = false
		}, true, []Change{{Op: "update", Kind: "provider", Name: "up-a", Fields: []string{"base_url", "allow_private_network"}}}},
		{
			"removed provider key with prune", func(f *File) { f.Providers[1].Keys = f.Providers[1].Keys[1:] }, true,
			[]Change{{Op: "update", Kind: "provider", Name: "up-a", Fields: []string{"keys"}}},
		},
		{"removed provider key without prune", func(f *File) { f.Providers[1].Keys = f.Providers[1].Keys[1:] }, false, []Change{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := populated()
			doc, _ := exportDoc(t, w)
			tt.edit(&doc)
			got, err := w.service().Plan(context.Background(), doc, nil, tt.prune)
			if err != nil {
				t.Fatalf("Plan: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("changes = %+v\nwant %+v", got, tt.want)
			}
			if calls := w.calls(); len(calls) != 0 {
				t.Errorf("Plan wrote: %v", calls)
			}
		})
	}
}

const applyOrderDoc = `
providers:
  - {name: p, kind: openai-compatible, base_url: http://upstream.test/p, keys: [{name: main, value: env:P_KEY}]}
aliases:
  - {name: b, kind: chat, members: [a]}
  - {name: a, kind: chat, members: [p/m]}
keys:
  - {name: k, value: env:K_KEY, allow: [b]}
`

func TestApplyOrder(t *testing.T) {
	w := newWorld()
	ctx := context.Background()
	sec := Secrets{"P_KEY": "sk-p", "K_KEY": "sk-k"}
	applied, err := w.service().Apply(ctx, mustParse(t, applyOrderDoc), sec, true)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	want := []string{"create provider p", "create alias a", "create alias b", "create key k"}
	if got := w.calls(); !reflect.DeepEqual(got, want) {
		t.Errorf("calls = %v, want %v", got, want)
	}
	if len(applied) != 4 || applied[3].Value != "" {
		t.Errorf("applied = %+v", applied)
	}

	// Pruning everything deletes in reverse: keys, then aliases (dependents first), then providers.
	w.log = nil
	if _, err := w.service().Apply(ctx, File{}, nil, true); err != nil {
		t.Fatalf("Apply(empty): %v", err)
	}
	want = []string{"delete key k", "delete alias b", "delete alias a", "delete provider p"}
	if got := w.calls(); !reflect.DeepEqual(got, want) {
		t.Errorf("prune calls = %v, want %v", got, want)
	}
}

func TestApplyFailureChangesNothing(t *testing.T) {
	w := newWorld()
	doc := mustParse(t, strings.Replace(applyOrderDoc, "members: [a]", "members: [nowhere]", 1))
	applied, err := w.service().Apply(context.Background(), doc, Secrets{"P_KEY": "sk-p", "K_KEY": "sk-k"}, true)
	var ae *ApplyError
	if !errors.As(err, &ae) || !errors.Is(err, alias.ErrUnknownMember) || ae.Change.Name != "b" {
		t.Fatalf("err = %v, want an *ApplyError for alias b", err)
	}
	if len(applied) != 0 {
		t.Errorf("applied = %+v, want none", applied)
	}
	if got := w.calls(); !reflect.DeepEqual(got, []string{"create provider p", "create alias a"}) {
		t.Errorf("calls = %v", got)
	}
	if len(w.providers) != 0 || len(w.aliases) != 0 {
		t.Errorf("providers %v, aliases %v; want the writes before the failure rolled back", w.providers, w.aliases)
	}
}

func TestApplyNewKeys(t *testing.T) {
	w := newWorld()
	doc := mustParse(t, `
keys:
  - {name: gen, allow: ["*"]}
  - {name: off, value: env:OFF_KEY, allow: ["*"], enabled: false}
`)
	applied, err := w.service().Apply(context.Background(), doc, Secrets{"OFF_KEY": "sk-off"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(applied) != 2 || applied[0].Name != "gen" || !strings.HasPrefix(applied[0].Value, "sk-generated-") || applied[1].Value != "" {
		t.Errorf("applied = %+v; want gen's generated value only", applied)
	}
	if k, _ := w.keyByName("off"); k.Enabled || w.values[k.ID] != "sk-off" {
		t.Errorf("off = %+v value %q", k, w.values[k.ID])
	}
	// The disabled key is created disabled in one write, never created enabled and then updated.
	if got := w.calls(); !reflect.DeepEqual(got, []string{"create key gen", "create key off"}) {
		t.Errorf("calls = %v", got)
	}
	// A plan never generates.
	if cs, _ := w.service().Plan(context.Background(), mustParse(t, "keys:\n  - {name: other, allow: []}\n"), nil, false); len(cs) != 1 || cs[0].Value != "" {
		t.Errorf("plan = %+v", cs)
	}
}

func TestApplyProviderKeysWithoutPrune(t *testing.T) {
	w := populated()
	doc, _ := exportDoc(t, w)
	doc.Providers[1].Keys = []FileSecretKey{{Name: "main", Value: "env:UP_MAIN"}}
	if _, err := w.service().Apply(context.Background(), doc, Secrets{"UP_MAIN": "sk-new-main"}, false); err != nil {
		t.Fatal(err)
	}
	got := w.providers["up-a"].Keys
	want := []ProviderKey{{Name: "main", Value: "sk-new-main"}, {Name: "backup", Value: "sk-up-stored-backup"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("up-a keys = %+v, want %+v (an unlisted key kept without prune)", got, want)
	}
}

func TestApplyNothingSkipsTheBatch(t *testing.T) {
	w := newWorld()
	s := NewService(fakeProviders{w}, fakeAliases{w}, fakeKeys{w}, fakeAccountStrategies{w}, func(context.Context, func(context.Context) error) error {
		return errors.New("engine unavailable")
	})
	if applied, err := s.Apply(context.Background(), File{}, nil, true); err != nil || len(applied) != 0 {
		t.Fatalf("applied %v, err %v; want a no-op apply to succeed without the engine", applied, err)
	}
}

func TestPlanOmittedStrategyMatchesPriority(t *testing.T) {
	w := populated()
	doc, _ := exportDoc(t, w)
	for _, a := range doc.Aliases {
		if a.Name == "fast" && a.Strategy != "" {
			t.Fatalf("fast exported with strategy %q, want omitted", a.Strategy)
		}
	}
	plan, err := w.service().Plan(context.Background(), doc, nil, true)
	if err != nil || len(plan) != 0 {
		t.Errorf("omitted vs stored priority: plan = %+v, %v", plan, err)
	}
	w.aliases["inner"] = alias.Alias{Name: "inner", Kind: alias.KindChat, Members: members("up-a/m1"), Strategy: alias.StrategyRandom}
	plan, err = w.service().Plan(context.Background(), doc, nil, true)
	if err != nil || !reflect.DeepEqual(plan, []Change{{Op: "update", Kind: "alias", Name: "inner", Fields: []string{"strategy"}}}) {
		t.Errorf("random vs round-robin: plan = %+v, %v", plan, err)
	}
}

func TestExportOmitsPriority(t *testing.T) {
	w := populated()
	w.aliases["emb"] = alias.Alias{Name: "emb", Kind: alias.KindEmbeddings, Members: members("up-a/emb-1"), Strategy: alias.StrategyP2C}
	doc, _ := exportDoc(t, w)
	got := map[string]string{}
	for _, a := range doc.Aliases {
		got[a.Name] = a.Strategy
	}
	if want := map[string]string{"emb": "p2c", "fast": "", "inner": "round-robin"}; !reflect.DeepEqual(got, want) {
		t.Errorf("exported strategies = %v, want %v", got, want)
	}
}

func TestPlanAccountStrategies(t *testing.T) {
	w := populated()
	doc, _ := exportDoc(t, w)
	doc.AccountStrategies = map[string]string{"codex": "p2c"}
	changes, err := w.service().Plan(context.Background(), doc, nil, true)
	if want := []Change{{Op: "update", Kind: "account_strategy", Name: "codex", Fields: []string{"strategy"}}}; err != nil || !reflect.DeepEqual(changes, want) {
		t.Errorf("differing: %+v, %v", changes, err)
	}
	w.strats["codex"] = alias.StrategyP2C
	if changes, err := w.service().Plan(context.Background(), doc, nil, true); err != nil || len(changes) != 0 {
		t.Errorf("equal: %+v, %v", changes, err)
	}
	w.strats["grok"] = alias.StrategyP2C
	doc.AccountStrategies = nil
	changes, err = w.service().Plan(context.Background(), doc, nil, true)
	want := []Change{
		{Op: "update", Kind: "account_strategy", Name: "codex", Fields: []string{"strategy"}},
		{Op: "update", Kind: "account_strategy", Name: "grok", Fields: []string{"strategy"}},
	}
	if err != nil || !reflect.DeepEqual(changes, want) {
		t.Errorf("prune resets: %+v, %v", changes, err)
	}
	if changes, err := w.service().Plan(context.Background(), doc, nil, false); err != nil || len(changes) != 0 {
		t.Errorf("without prune an omitted provider is kept: %+v, %v", changes, err)
	}
}

func TestApplyAccountStrategyAtomic(t *testing.T) {
	w := populated()
	doc, _ := exportDoc(t, w)
	doc.Aliases[1].ContextWindow = 1000 // fast
	doc.AccountStrategies = map[string]string{"codex": "least-used"}
	doc.Keys = doc.Keys[:2] // prune deletes "off" after the strategy is written
	w.failOn = "delete key off"
	if _, err := w.service().Apply(context.Background(), doc, nil, true); err == nil {
		t.Fatal("Apply succeeded")
	}
	if w.aliases["fast"].ContextWindow != 131072 || len(w.strats) != 0 || w.nudged != 0 {
		t.Errorf("after a failed apply: fast %+v, strategies %v, nudged %d", w.aliases["fast"], w.strats, w.nudged)
	}
	w.failOn = ""
	if _, err := w.service().Apply(context.Background(), doc, nil, true); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if w.strats["codex"] != alias.StrategyLeastUsed || w.nudged != 1 {
		t.Errorf("after apply: strategies %v, nudged %d", w.strats, w.nudged)
	}
}

func TestExportAccountStrategies(t *testing.T) {
	w := populated()
	if doc, _ := exportDoc(t, w); doc.AccountStrategies != nil {
		t.Errorf("none set: %v", doc.AccountStrategies)
	}
	w.strats = map[string]alias.Strategy{"codex": alias.StrategyP2C, "grok": alias.StrategyRoundRobin}
	doc, b := exportDoc(t, w)
	if !reflect.DeepEqual(doc.AccountStrategies, map[string]string{"codex": "p2c"}) {
		t.Errorf("exported %v", doc.AccountStrategies)
	}
	if err := checkSchema(b); err != nil {
		t.Errorf("schema: %v", err)
	}
}
