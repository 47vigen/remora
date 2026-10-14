package cliproxyapi_test

import (
	"testing"

	"github.com/47vigen/remora/internal/engine/bifrost"
	"github.com/47vigen/remora/internal/engine/cliproxyapi"
)

func TestProviderTable(t *testing.T) {
	ps := cliproxyapi.Providers()
	if len(ps) != 8 {
		t.Fatalf("len(Providers()) = %d, want 8", len(ps))
	}
	want := map[string]cliproxyapi.Provider{
		"antigravity": {Name: "antigravity", Engine: "antigravity", Mode: "paste"},
		"claude":      {Name: "claude", Engine: "claude", Mode: "paste"},
		"codex":       {Name: "codex", Engine: "codex", Mode: "paste"},
		"devin":       {Name: "devin", Engine: "devin", Mode: "paste"},
		"grok":        {Name: "grok", Engine: "xai", Mode: "device"},
		"kimi":        {Name: "kimi", Engine: "kimi", Mode: "device"},
		"kimi-ai":     {Name: "kimi-ai", Engine: "kimi-ai", Mode: "device"},
		"meta":        {Name: "meta", Engine: "meta", Mode: "device"},
	}
	for _, p := range ps {
		if w, ok := want[p.Name]; !ok || w != p {
			t.Errorf("provider %+v, want %+v", p, w)
		}
		if bifrost.IsStandardProvider(p.Name) {
			t.Errorf("provider %q is a front-door standard name", p.Name)
		}
		if got, ok := cliproxyapi.ProviderByName(p.Name); !ok || got != p {
			t.Errorf("ProviderByName(%q) = %+v, %v", p.Name, got, ok)
		}
		if got, ok := cliproxyapi.ProviderByEngine(p.Engine); !ok || got != p {
			t.Errorf("ProviderByEngine(%q) = %+v, %v", p.Engine, got, ok)
		}
	}
	if p, _ := cliproxyapi.ProviderByName("grok"); p.Engine != "xai" {
		t.Errorf("grok engine = %q, want xai", p.Engine)
	}
	if p, _ := cliproxyapi.ProviderByEngine("xai"); p.Name != "grok" {
		t.Errorf("ProviderByEngine(xai).Name = %q, want grok", p.Name)
	}
	for _, name := range []string{"xai", "openai", "", "CODEX"} {
		if _, ok := cliproxyapi.ProviderByName(name); ok {
			t.Errorf("ProviderByName(%q) found a provider", name)
		}
	}
	if _, ok := cliproxyapi.ProviderByEngine("grok"); ok {
		t.Error("ProviderByEngine(grok) found a provider; the engine calls it xai")
	}

	// The table is a copy: changing it does not change the next call.
	ps[0].Name = "changed"
	if cliproxyapi.Providers()[0].Name == "changed" {
		t.Error("Providers() shares its backing array")
	}
}

func TestUserRef(t *testing.T) {
	tests := []struct{ provider, model, want string }{
		{bifrost.OAuthProvider, "codex/gpt-x", "codex/gpt-x"},
		{"openrouter", "a/b", "openrouter/a/b"},
		{"up-a", "m", "up-a/m"},
	}
	for _, tt := range tests {
		if got := cliproxyapi.UserRef(tt.provider, tt.model); got != tt.want {
			t.Errorf("UserRef(%q, %q) = %q, want %q", tt.provider, tt.model, got, tt.want)
		}
	}
}
