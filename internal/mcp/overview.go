package mcp

import (
	"context"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/47vigen/remora/internal/api/gen"
)

// Overview is remora_overview's answer. Engine versions are left out: engine names stay in /api/system/version.
type Overview struct {
	Status     string              `json:"status"`
	Components map[string]string   `json:"components"`
	Version    string              `json:"version"`
	Providers  int                 `json:"providers"`
	Aliases    int                 `json:"aliases"`
	Keys       int                 `json:"keys"`
	Accounts   int                 `json:"accounts"`
	OutOfPool  []gen.AccountPool   `json:"out_of_pool"`
	Cooling    int                 `json:"cooling"`
	Quota      []gen.QuotaProvider `json:"quota"` // per provider, account rows dropped
}

func addOverview(s *sdk.Server, c gen.ClientWithResponsesInterface) {
	add(s, &sdk.Tool{
		Name: "remora_overview",
		Description: "Start here: health, version, counts of providers, aliases, keys and accounts, accounts out of " +
			"the pool, cooling members, quota per subscription provider.",
		Annotations: readOnly,
	}, func(ctx context.Context, _ struct{}) (Overview, error) { return overview(ctx, c) })
}

func overview(ctx context.Context, c gen.ClientWithResponsesInterface) (Overview, error) {
	var o Overview
	one := 1
	health, err := ok200(c.GetHealthWithResponse(ctx))
	if err != nil {
		return o, err
	}
	o.Status = string(health.Status)
	o.Components = map[string]string{}
	for k, v := range health.Engines {
		o.Components[k] = string(v)
	}
	v, err := ok200(c.GetVersionWithResponse(ctx))
	if err != nil {
		return o, err
	}
	o.Version = v.Remora
	providers, err := ok200(c.ListProvidersWithResponse(ctx, &gen.ListProvidersParams{Limit: &one}))
	if err != nil {
		return o, err
	}
	o.Providers = providers.Total
	aliases, err := ok200(c.ListAliasesWithResponse(ctx, &gen.ListAliasesParams{Limit: &one}))
	if err != nil {
		return o, err
	}
	o.Aliases = aliases.Total
	keys, err := ok200(c.ListKeysWithResponse(ctx, &gen.ListKeysParams{Limit: &one}))
	if err != nil {
		return o, err
	}
	o.Keys = keys.Total
	accounts, err := ok200(c.ListAccountsWithResponse(ctx, &gen.ListAccountsParams{Limit: &one}))
	if err != nil {
		return o, err
	}
	o.Accounts = accounts.Total
	pool, err := ok200(c.GetAccountPoolWithResponse(ctx))
	if err != nil {
		return o, err
	}
	o.OutOfPool = []gen.AccountPool{}
	for _, p := range *pool {
		if len(p.Out) > 0 {
			o.OutOfPool = append(o.OutOfPool, p)
		}
	}
	cooling, err := ok200(c.ListCoolingWithResponse(ctx, &gen.ListCoolingParams{Limit: &one}))
	if err != nil {
		return o, err
	}
	o.Cooling = cooling.Total
	quota, err := ok200(c.GetQuotaWithResponse(ctx))
	if err != nil {
		return o, err
	}
	o.Quota = *quota
	for i := range o.Quota {
		o.Quota[i].Accounts = nil
	}
	return o, nil
}
