package reconcile

import (
	"strconv"

	"github.com/47vigen/remora/internal/alias"
	"github.com/47vigen/remora/internal/engine/bifrost"
	"github.com/47vigen/remora/internal/engine/cliproxyapi"
)

// oauthRulePriorityBase is the priority of the first OAuth routing rule; rule i takes oauthRulePriorityBase+i. They
// stay below RulePriorityBase, so no alias rule ever takes one. An OAuth rule matches "<name>/..." and an alias name
// never starts with a provider name, so the order against alias rules does not matter.
const oauthRulePriorityBase = 100

// oauthKeyID is the id (and name) of the OAuth engine's one key in the front door.
const oauthKeyID = "remora-oauth-key"

// oauthAllowed are the request types the front door may send to the OAuth engine. /v1/messages needs none of its
// own: the front door turns it into a Responses request.
var oauthAllowed = []string{"list_models", "chat_completion", "chat_completion_stream", "responses", "responses_stream"}

// OAuthProvider is the OAuth engine as the front door's internal custom provider. engineURL has no /v1: the front
// door appends /v1/... itself. The engine listens on loopback, so private networks are allowed.
func OAuthProvider(clientKey, engineURL string) InternalProvider {
	return InternalProvider{
		Provider: bifrost.Provider{
			Name:                bifrost.OAuthProvider,
			BaseType:            "openai",
			BaseURL:             engineURL,
			AllowPrivateNetwork: true,
			AllowedRequests:     append([]string(nil), oauthAllowed...),
		},
		Keys: []bifrost.Key{{ID: oauthKeyID, Name: oauthKeyID, Value: clientKey, Enabled: true}},
	}
}

// OAuthRules returns one routing rule per subscription provider: a request for "<name>/<model>" goes to the OAuth
// engine with the incoming model, which the engine routes by its credential prefix. Each rule has its own priority
// below RulePriorityBase.
func OAuthRules() []bifrost.RoutingRule {
	provs := cliproxyapi.Providers()
	out := make([]bifrost.RoutingRule, 0, len(provs))
	for i, p := range provs {
		out = append(out, bifrost.RoutingRule{
			Name:     bifrost.OAuthProvider + "-" + p.Name,
			CEL:      "model.startsWith(" + strconv.Quote(p.Name+"/") + ")",
			Target:   bifrost.Route{Provider: bifrost.OAuthProvider},
			Priority: oauthRulePriorityBase + i,
		})
	}
	return out
}

// OAuthReserved returns every subscription provider name, for Reconciler.Reserved.
func OAuthReserved() []string {
	provs := cliproxyapi.Providers()
	out := make([]string, 0, len(provs))
	for _, p := range provs {
		out = append(out, p.Name)
	}
	return out
}

// ToEngineTarget maps an alias target on a subscription provider to the OAuth engine's front-door provider, with the
// model the engine routes on ("<name>/<model>"). Any other target is returned unchanged.
func ToEngineTarget(t alias.Target) alias.Target {
	if _, ok := cliproxyapi.ProviderByName(t.Provider); ok {
		return alias.Target{Provider: bifrost.OAuthProvider, Model: t.Provider + "/" + t.Model}
	}
	return t
}
