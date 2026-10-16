package config

import (
	"reflect"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	src := `
version: 1
providers:
  - name: up-a
    kind: openai-compatible
    base_url: http://fakes:9090/u/a
    allow_private_network: true
    keys: [{name: main, value: env:E2E_UPSTREAM_KEY}]
  - {name: router, kind: builtin, type: openrouter, keys: [{name: k1, value: env:OR_KEY}]}
aliases:
  - {name: memory/fast, kind: chat, members: [inner, up-c/m1], capabilities: [tools], context_window: 131072, strategy: round-robin}
keys:
  - {name: full, value: env:E2E_KEY_FULL, allow: ["*"]}
  - {name: off, allow: ["*"], enabled: false}
`
	got, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	off := false
	want := File{
		Version: 1,
		Providers: []FileProvider{
			{
				Name: "up-a", Kind: "openai-compatible", BaseURL: "http://fakes:9090/u/a", AllowPrivateNetwork: true,
				Keys: []FileSecretKey{{Name: "main", Value: "env:E2E_UPSTREAM_KEY"}},
			},
			{Name: "router", Kind: "builtin", Type: "openrouter", Keys: []FileSecretKey{{Name: "k1", Value: "env:OR_KEY"}}},
		},
		Aliases: []FileAlias{{
			Name: "memory/fast", Kind: "chat", Members: []string{"inner", "up-c/m1"},
			Capabilities: []string{"tools"}, ContextWindow: 131072, Strategy: "round-robin",
		}},
		Keys: []FileKey{
			{Name: "full", Value: "env:E2E_KEY_FULL", Allow: []string{"*"}},
			{Name: "off", Allow: []string{"*"}, Enabled: &off},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Parse =\n %+v\nwant\n %+v", got, want)
	}

	if f, err := Parse(nil); err != nil || !reflect.DeepEqual(f, File{}) {
		t.Errorf("Parse(empty) = (%+v, %v), want an empty file", f, err)
	}
}

func TestParse_Errors(t *testing.T) {
	base := "providers:\n  - {name: up-a, kind: openai-compatible, keys: [{name: main, value: env:K}]}\n"
	tests := []struct {
		name  string
		src   string
		wants []string
	}{
		{"unknown top-level field", "provider: []\n", []string{"provider"}},
		{"unknown nested field", "providers:\n  - {name: up-a, kind: builtin, type: openai, colour: red}\n", []string{"colour", "line 2"}},
		{"unknown key field", "keys:\n  - {name: a, allow: ['*'], secret: x}\n", []string{"secret"}},
		{"version", "version: 2\n", []string{"version", "2"}},
		{"not yaml", "providers: [\n", []string{"remora.yaml"}},
		{"literal secret", strings.Replace(base, "env:K", "sk-literal", 1), []string{"providers[0].keys[0].value", "env:"}},
		{"empty env name", strings.Replace(base, "env:K", "env:", 1), []string{"providers[0].keys[0].value"}},
		{"bad kind", strings.Replace(base, "openai-compatible", "magic", 1), []string{"providers[0].kind", "magic"}},
		{"builtin without type", "providers:\n  - {name: x, kind: builtin}\n", []string{"providers[0].type"}},
		{"type on custom", "providers:\n  - {name: x, kind: openai-compatible, type: openai}\n", []string{"providers[0].type"}},
		{"duplicate provider", base + "  - {name: up-a, kind: openai-compatible}\n", []string{"providers[1].name", "up-a"}},
		{"duplicate provider key", "providers:\n  - {name: p, kind: openai-compatible, keys: [{name: a, value: env:A}, {name: a, value: env:B}]}\n", []string{"providers[0].keys[1].name"}},
		{"missing provider name", "providers:\n  - {kind: openai-compatible}\n", []string{"providers[0].name"}},
		{"bad alias kind", "aliases:\n  - {name: a, kind: video, members: [p/m]}\n", []string{"aliases[0].kind", "video"}},
		{"bad alias name", "aliases:\n  - {name: -a, kind: chat, members: [p/m]}\n", []string{"aliases[0].name"}},
		{"alias without members", "aliases:\n  - {name: a, kind: chat}\n", []string{"aliases[0].members"}},
		{"negative context", "aliases:\n  - {name: a, kind: chat, members: [p/m], context_window: -1}\n", []string{"aliases[0].context_window"}},
		{"bad strategy", "aliases:\n  - {name: a, kind: chat, members: [p/m], strategy: fallback}\n", []string{`aliases[0].strategy: "fallback" is not one of priority, fill-first, round-robin, random, p2c, least-used`}},
		{"thinking on an api-key member", "aliases:\n  - {name: a, kind: chat, members: [up-a/m1(low)]}\n", []string{"aliases[0].members[0]", "subscription models"}},
		{"unknown thinking level", "aliases:\n  - {name: a, kind: chat, members: [antigravity/x(extreme)]}\n", []string{"aliases[0].members[0]", "extreme"}},
		{"duplicate alias", "aliases:\n  - {name: a, kind: chat, members: [p/m]}\n  - {name: a, kind: chat, members: [p/m]}\n", []string{"aliases[1].name"}},
		{"literal client key", "keys:\n  - {name: a, value: sk-x, allow: ['*']}\n", []string{"keys[0].value", "env:"}},
		{"duplicate client key", "keys:\n  - {name: a}\n  - {name: a}\n", []string{"keys[1].name"}},
		{"missing client key name", "keys:\n  - {allow: ['*']}\n", []string{"keys[0].name"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.src))
			if err == nil {
				t.Fatal("no error")
			}
			for _, w := range tt.wants {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q does not mention %q", err, w)
				}
			}
		})
	}
}

func TestSecretRef(t *testing.T) {
	for in, want := range map[string]string{"env:A": "A", "env:E2E_KEY": "E2E_KEY", "env:": "", "A": "", "env:a b": ""} {
		if got, _ := SecretRef(in); got != want {
			t.Errorf("SecretRef(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseRejectsSecondDocument(t *testing.T) {
	src := "---\n---\nproviders:\n  - {name: up, kind: openai-compatible, base_url: \"http://up.test/v1\"}\n"
	if _, err := Parse([]byte(src)); err == nil || !strings.Contains(err.Error(), "more than one YAML document") {
		t.Fatalf("Parse = %v, want the one-document error", err)
	}
}

func TestValidateStrategy(t *testing.T) {
	for _, st := range append([]string{""}, "priority", "fill-first", "round-robin", "random", "p2c", "least-used") {
		f := File{Aliases: []FileAlias{{Name: "a", Kind: "chat", Members: []string{"p/m"}, Strategy: st}}}
		if err := f.Validate(); err != nil {
			t.Errorf("strategy %q: %v", st, err)
		}
	}
	f := File{Aliases: []FileAlias{{Name: "a", Kind: "chat", Members: []string{"p/m"}, Strategy: "fallback"}}}
	if err := f.Validate(); err == nil || !strings.Contains(err.Error(), "aliases[0].strategy") {
		t.Errorf("strategy fallback: err = %v", err)
	}
}

func TestValidateAccountStrategies(t *testing.T) {
	if err := (File{AccountStrategies: map[string]string{"grok": "p2c"}}).Validate(); err != nil {
		t.Errorf("grok p2c: %v", err)
	}
	err := File{AccountStrategies: map[string]string{"xai": "p2c"}}.Validate()
	if err == nil || !strings.Contains(err.Error(), `account_strategies.xai: "xai" is not a subscription provider`) {
		t.Errorf("xai: err = %v", err)
	}
	err = File{AccountStrategies: map[string]string{"codex": "bogus"}}.Validate()
	if err == nil || !strings.Contains(err.Error(), `account_strategies.codex.strategy`) && !strings.Contains(err.Error(), `account_strategies.codex: "bogus" is not one of`) {
		t.Errorf("bogus: err = %v", err)
	}
	if _, err := Parse([]byte("account_strategies: {codex: least-used}\n")); err != nil {
		t.Errorf("Parse: %v", err)
	}
	if _, err := Parse([]byte("account_strategies: {codex: bogus}\n")); err == nil {
		t.Error("Parse accepted a bogus strategy")
	}
}
