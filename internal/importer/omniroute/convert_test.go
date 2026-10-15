package omniroute

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/47vigen/remora/internal/config"
)

func convertFixture(t *testing.T) Result {
	t.Helper()
	dir := t.TempDir()
	buildFixture(t, dir)
	s, err := Read(context.Background(), dir, noEnv, fixtureNow)
	if err != nil {
		t.Fatal(err)
	}
	return Convert(s, fixtureNow)
}

func hasWarning(r Result, want string) bool {
	return slices.ContainsFunc(r.Warnings, func(w string) bool { return strings.Contains(w, want) })
}

func TestConvertProviders(t *testing.T) {
	r := convertFixture(t)
	want := []config.FileProvider{
		{Name: "openrouter", Kind: "builtin", Type: "openrouter", Keys: []config.FileSecretKey{
			{Name: "key-1", Value: "env:REMORA_PROVIDER_OPENROUTER_KEY_1"},
			{Name: "key-2", Value: "env:REMORA_PROVIDER_OPENROUTER_KEY_2"},
		}},
		{Name: "oc", Kind: "openai-compatible", BaseURL: "https://llm.example.com/v1", Keys: []config.FileSecretKey{
			{Name: "key-1", Value: "env:REMORA_PROVIDER_OC_KEY_1"},
		}},
		{Name: "opencode-go", Kind: "openai-compatible", BaseURL: "https://opencode.ai/zen/go/v1", Keys: []config.FileSecretKey{
			{Name: "key-1", Value: "env:REMORA_PROVIDER_OPENCODE_GO_KEY_1"},
		}},
		{Name: "soniox", Kind: "soniox", Keys: []config.FileSecretKey{
			{Name: "key-1", Value: "env:REMORA_PROVIDER_SONIOX_KEY_1"},
		}},
	}
	if !reflect.DeepEqual(r.Doc.Providers, want) {
		t.Fatalf("providers:\n got %+v\nwant %+v", r.Doc.Providers, want)
	}
	for name, v := range map[string]string{
		"REMORA_PROVIDER_OPENROUTER_KEY_1":  fxORKey1,
		"REMORA_PROVIDER_OPENROUTER_KEY_2":  fxORKey2,
		"REMORA_PROVIDER_OC_KEY_1":          fxOCKey,
		"REMORA_PROVIDER_OPENCODE_GO_KEY_1": fxGoKey,
		"REMORA_PROVIDER_SONIOX_KEY_1":      fxSXKey,
	} {
		if r.Secrets[name] != v {
			t.Errorf("Secrets[%s] = %q, want %q", name, r.Secrets[name], v)
		}
	}
	if slices.ContainsFunc(mapValues(r.Secrets), func(v string) bool { return v == "sk-or-off" }) {
		t.Error("an inactive connection's key was imported")
	}
	if !hasWarning(r, "skipped mystery connection mx: not supported") {
		t.Errorf("an unknown provider was not skipped: %q", r.Warnings)
	}
	if !hasWarning(r, "skipped qwen account b@example.com: not supported") {
		t.Errorf("warnings: %q", r.Warnings)
	}
	if err := r.Doc.Validate(); err != nil {
		t.Fatalf("the document does not validate: %v", err)
	}
}

func mapValues(m map[string]string) []string {
	var out []string
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

func TestConvertKeysVerbatim(t *testing.T) {
	r := convertFixture(t)
	keys := map[string]config.FileKey{}
	for _, k := range r.Doc.Keys {
		keys[k.Name] = k
	}
	if k := keys["svc-a"]; k.Value != "env:REMORA_KEY_SVC_A" || !slices.Equal(k.Allow, []string{"*"}) || k.Enabled != nil {
		t.Fatalf("svc-a: %+v", k)
	}
	if r.Secrets["REMORA_KEY_SVC_A"] != fxClientKeyA || r.Secrets["REMORA_KEY_SVC_B"] != fxClientKeyB {
		t.Fatalf("client key values are not verbatim: %q", r.Secrets)
	}
	if _, ok := keys["old"]; ok || !hasWarning(r, "skipped key old: revoked") {
		t.Fatalf("revoked key: keys %+v, warnings %q", keys, r.Warnings)
	}
	if k := keys["svc-b"]; !slices.Equal(k.Allow, []string{"oc/model-x", "openrouter/vendor/model-a", "fast"}) {
		t.Fatalf("svc-b allow: %q", k.Allow)
	}
	if k := keys["temp"]; k.Enabled == nil || *k.Enabled {
		t.Fatalf("expired key should be disabled: %+v", k)
	}
}

func TestConvertKeyAllCombos(t *testing.T) {
	s := Snapshot{
		Combos: []Combo{{Name: "a", Data: json.RawMessage(`{"models":["p/m"]}`)}, {Name: "b", Data: json.RawMessage(`{"models":["a"]}`)}},
		Keys: []APIKey{
			{Name: "k", Key: "sk-x", AccessMode: "restricted", AllowedModels: []string{"p/m"}, AllowedCombos: []string{"combo/*"}},
			{Name: "k", Key: "sk-y", AccessMode: "all"},
		},
	}
	r := Convert(s, fixtureNow)
	if len(r.Doc.Keys) != 2 || !slices.Equal(r.Doc.Keys[0].Allow, []string{"p/m", "a", "b"}) {
		t.Fatalf("keys: %+v", r.Doc.Keys)
	}
	if r.Doc.Keys[1].Name == "k" || r.Secrets[config.EnvName("", r.Doc.Keys[1].Name)] != "sk-y" || !hasWarning(r, "renamed") {
		t.Fatalf("a duplicate name must be renamed: %+v %q", r.Doc.Keys, r.Warnings)
	}
}

func TestConvertCombos(t *testing.T) {
	nodes := []Node{{ID: "openai-compatible-chat-n1", Type: "openai-compatible", Prefix: "oc", BaseURL: "https://llm.example.com/v1"}}
	for _, c := range []struct {
		name, data string
		want       config.FileAlias
		warning    string
	}{
		{
			"provider model string", `{"models":["openrouter/vendor/model-a"]}`,
			config.FileAlias{Name: "x", Kind: "chat", Members: []string{"openrouter/vendor/model-a"}},
			"",
		},
		{
			"alias name string", `{"models":["base"]}`,
			config.FileAlias{Name: "x", Kind: "chat", Members: []string{"base"}},
			"",
		},
		{
			"combo-ref", `{"models":[{"kind":"combo-ref","comboName":"inner"}]}`,
			config.FileAlias{Name: "x", Kind: "chat", Members: []string{"inner"}},
			"",
		},
		{
			"model with slash", `{"models":[{"model":"openrouter/vendor/m","providerId":"ignored"}]}`,
			config.FileAlias{Name: "x", Kind: "chat", Members: []string{"openrouter/vendor/m"}},
			"",
		},
		{
			"model with provider id", `{"models":[{"model":"m","providerId":"openrouter"}]}`,
			config.FileAlias{Name: "x", Kind: "chat", Members: []string{"openrouter/m"}},
			"",
		},
		{
			"model with node id", `{"models":[{"model":"m","providerId":"openai-compatible-chat-n1"}]}`,
			config.FileAlias{Name: "x", Kind: "chat", Members: []string{"oc/m"}},
			"",
		},
		{
			"context length", `{"models":["p/m"],"context_length":128000,"strategy":"priority"}`,
			config.FileAlias{Name: "x", Kind: "chat", Members: []string{"p/m"}, ContextWindow: 128000},
			"",
		},
		{
			"dimensions", `{"models":["p/e"],"dimensions":1024}`,
			config.FileAlias{Name: "x", Kind: "embeddings", Members: []string{"p/e"}},
			"",
		},
		{
			"strategy", `{"models":["p/m","q/m"],"strategy":"round-robin"}`,
			config.FileAlias{Name: "x", Kind: "chat", Members: []string{"p/m", "q/m"}},
			"alias x: strategy round-robin becomes ordered fallback",
		},
		{
			"unknown provider kept", `{"models":["nowhere/m"]}`,
			config.FileAlias{Name: "x", Kind: "chat", Members: []string{"nowhere/m"}},
			"",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := Convert(Snapshot{Nodes: nodes, Combos: []Combo{{Name: "x", Data: json.RawMessage(c.data)}}}, fixtureNow)
			if len(r.Doc.Aliases) != 1 || !reflect.DeepEqual(r.Doc.Aliases[0], c.want) {
				t.Fatalf("got %+v, want %+v", r.Doc.Aliases, c.want)
			}
			if c.warning != "" && !hasWarning(r, c.warning) {
				t.Fatalf("warnings %q, want %q", r.Warnings, c.warning)
			}
			if c.warning == "" && len(r.Warnings) != 0 {
				t.Fatalf("unexpected warnings %q", r.Warnings)
			}
		})
	}

	// The fixture: nesting through combo-ref, and every alias validates.
	r := convertFixture(t)
	aliases := map[string]config.FileAlias{}
	for _, a := range r.Doc.Aliases {
		aliases[a.Name] = a
	}
	if a := aliases["fast"]; !slices.Equal(a.Members, []string{"openrouter/vendor/model-a", "oc/model-b", "openrouter/vendor/model-c", "base"}) || a.ContextWindow != 128000 {
		t.Fatalf("fast: %+v", a)
	}
	if aliases["embed"].Kind != "embeddings" {
		t.Fatalf("embed: %+v", aliases["embed"])
	}
}
