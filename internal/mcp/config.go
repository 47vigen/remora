package mcp

import (
	"context"
	"net/http"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/47vigen/remora/internal/api/gen"
)

// ConfigIn is remora_config_plan's input.
type ConfigIn struct {
	Document string            `json:"document" jsonschema:"remora.yaml, whole or partial"`
	Secrets  map[string]string `json:"secrets,omitempty" jsonschema:"values for env:NAME references, only for new or rotated secrets; an absent name keeps the stored secret"`
	Prune    bool              `json:"prune,omitempty" jsonschema:"default false: add or update only what the document names. true deletes everything the document leaves out"`
}

// ConfigApplyIn is remora_config_apply's input.
type ConfigApplyIn struct {
	ConfigIn
	PlanHash string `json:"plan_hash" jsonschema:"the plan_hash remora_config_plan returned for these same inputs"`
}

// ConfigPlanOut is remora_config_plan's answer.
type ConfigPlanOut struct {
	Changes  []gen.Change `json:"changes"`
	PlanHash string       `json:"plan_hash"`
}

// ConfigApplyOut is remora_config_apply's answer.
type ConfigApplyOut struct {
	Applied  bool         `json:"applied"`
	Changes  []gen.Change `json:"changes"`             // made, or on a refusal the fresh plan
	PlanHash string       `json:"plan_hash,omitempty"` // set on a refusal
	Message  string       `json:"message,omitempty"`
}

const staleMessage = "The configuration changed since the plan. Nothing was applied. Review these changes and apply " +
	"again with this plan_hash."

func addConfig(s *sdk.Server, c gen.ClientWithResponsesInterface) {
	add(s, &sdk.Tool{
		Name: "remora_config_plan",
		Description: "What a remora.yaml document (whole or partial) would change, and the plan_hash to apply it with. " +
			"Providers, aliases, keys and both strategy kinds change only through plan and apply. Show the changes to " +
			"the user first.",
		Annotations: readOnly,
	}, func(ctx context.Context, in ConfigIn) (ConfigPlanOut, error) {
		cs, err := ok200(c.PlanConfigWithResponse(ctx, request(in, "")))
		if err != nil {
			return ConfigPlanOut{}, err
		}
		out := ConfigPlanOut{Changes: cs.Changes}
		if cs.PlanHash != nil {
			out.PlanHash = *cs.PlanHash
		}
		return out, nil
	})

	add(s, &sdk.Tool{
		Name: "remora_config_apply",
		Description: "Apply a planned document. Refuses with the fresh plan when the configuration changed since. " +
			"Generated key values appear once.",
		Annotations: write(true),
	}, func(ctx context.Context, in ConfigApplyIn) (ConfigApplyOut, error) {
		if in.PlanHash == "" {
			return ConfigApplyOut{}, toolError("plan_hash is required: plan first with remora_config_plan.")
		}
		r, err := c.ApplyConfigWithResponse(ctx, request(in.ConfigIn, in.PlanHash))
		if err != nil {
			return ConfigApplyOut{}, err
		}
		// A refusal is a normal result, so the agent gets the fresh plan to show.
		if r.StatusCode() == http.StatusConflict && r.JSON409 != nil {
			out := ConfigApplyOut{Changes: r.JSON409.Changes, Message: staleMessage}
			if r.JSON409.PlanHash != nil {
				out.PlanHash = *r.JSON409.PlanHash
			}
			return out, nil
		}
		cs, err := ok200(r, nil)
		if err != nil {
			return ConfigApplyOut{}, err
		}
		return ConfigApplyOut{Applied: true, Changes: cs.Changes}, nil
	})
}

// request always sends prune and secrets: the API's prune default is true, the tools' is false.
func request(in ConfigIn, hash string) gen.ConfigRequest {
	sec := in.Secrets
	if sec == nil {
		sec = map[string]string{}
	}
	return gen.ConfigRequest{Document: in.Document, Secrets: &sec, Prune: &in.Prune, PlanHash: opt(hash)}
}
