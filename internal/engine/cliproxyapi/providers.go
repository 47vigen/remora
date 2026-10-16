package cliproxyapi

import "github.com/47vigen/remora/internal/engine/bifrost"

// Provider is one subscription provider remora offers. Name is what users see and the model prefix they send
// ("codex/gpt-x"); Engine is the OAuth engine's provider id; Mode is "paste" (the user pastes the address the
// browser ends on) or "device" (the user enters a code on the provider's page).
type Provider struct{ Name, Engine, Mode string }

// providers is the fixed table. The engine ids are the ones GET /oauth/auth-url dispatches on; the device flows
// are the ones that answer "flow": "device". The engine's "xai" is shown as "grok" because "xai" is a standard
// front-door provider name. No Name may be a front-door standard provider name.
var providers = [...]Provider{
	{Name: "antigravity", Engine: "antigravity", Mode: "paste"},
	{Name: "claude", Engine: "claude", Mode: "paste"},
	{Name: "codex", Engine: "codex", Mode: "paste"},
	{Name: "devin", Engine: "devin", Mode: "paste"},
	{Name: "grok", Engine: "xai", Mode: "device"},
	{Name: "kimi", Engine: "kimi", Mode: "device"},
	{Name: "kimi-ai", Engine: "kimi-ai", Mode: "device"},
	{Name: "meta", Engine: "meta", Mode: "device"},
}

// Providers returns a copy of the provider table, ordered by name.
func Providers() []Provider {
	out := make([]Provider, len(providers))
	copy(out, providers[:])
	return out
}

// IsSubscription reports whether name is a subscription provider.
func IsSubscription(name string) bool {
	_, ok := ProviderByName(name)
	return ok
}

// ProviderByName finds a provider by its user-facing name.
func ProviderByName(name string) (Provider, bool) {
	for _, p := range providers {
		if p.Name == name {
			return p, true
		}
	}
	return Provider{}, false
}

// ProviderByEngine finds a provider by the engine's provider id.
func ProviderByEngine(engine string) (Provider, bool) {
	for _, p := range providers {
		if p.Engine == engine {
			return p, true
		}
	}
	return Provider{}, false
}

// UserRef is the reference a user sees for a model the front door served. For the internal OAuth provider the model
// is already "<name>/<model>" (every OAuth credential carries its provider name as prefix); otherwise it is
// "<provider>/<model>".
func UserRef(provider, model string) string {
	if provider == bifrost.OAuthProvider {
		return model
	}
	return provider + "/" + model
}
