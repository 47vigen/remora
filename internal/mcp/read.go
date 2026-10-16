package mcp

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/47vigen/remora/internal/api/gen"
)

// InspectIn is remora_inspect's input.
type InspectIn struct {
	Kind   string `json:"kind" jsonschema:"providers|accounts|aliases|keys|tokens|cooling|quota|account_strategies|logins"`
	ID     string `json:"id,omitempty" jsonschema:"name or id; omitted lists. For logins the login state, required. For quota a provider name"`
	Q      string `json:"q,omitempty" jsonschema:"free-text search when listing"`
	Limit  int    `json:"limit,omitempty" jsonschema:"page size when listing, default 50"`
	Offset int    `json:"offset,omitempty"`
}

// LogsIn is remora_logs' input.
type LogsIn struct {
	ID       string `json:"id,omitempty" jsonschema:"one entry by id; the filters are ignored"`
	From     string `json:"from,omitempty" jsonschema:"RFC 3339; default 24 hours before to"`
	To       string `json:"to,omitempty" jsonschema:"RFC 3339; default now"`
	Q        string `json:"q,omitempty"`
	Key      string `json:"key,omitempty"`
	Alias    string `json:"alias,omitempty"`
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	Status   string `json:"status,omitempty" jsonschema:"success|error"`
	Limit    int    `json:"limit,omitempty" jsonschema:"default 20"`
	Offset   int    `json:"offset,omitempty"`
}

// StatsIn is remora_stats' input.
type StatsIn struct {
	From     string `json:"from,omitempty" jsonschema:"RFC 3339; default 24 hours before to"`
	To       string `json:"to,omitempty" jsonschema:"RFC 3339; default now"`
	GroupBy  string `json:"group_by,omitempty" jsonschema:"none|key|alias|provider|model"`
	Key      string `json:"key,omitempty"`
	Alias    string `json:"alias,omitempty"`
	Provider string `json:"provider,omitempty"`
}

// ProviderTestIn is remora_provider_test's input.
type ProviderTestIn struct {
	Name string `json:"name" jsonschema:"the provider's name"`
}

// ConfigExportOut is remora_config_export's answer.
type ConfigExportOut struct {
	Document string `json:"document"`
}

// maxBody bounds each log body in a tool result; clients cap a result near 25k tokens.
const maxBody = 4096

const inspectKinds = "providers, accounts, aliases, keys, tokens, cooling, quota, account_strategies, logins"

func addRead(s *sdk.Server, c *gen.ClientWithResponses) {
	add(s, &sdk.Tool{
		Name: "remora_inspect",
		Description: "List or show one: providers, accounts, aliases (with the compiled chain and cooling), keys " +
			"(with usage), tokens (listed only), cooling, quota, account_strategies, logins (by state).",
		Annotations: readOnly,
	}, func(ctx context.Context, in InspectIn) (any, error) { return inspect(ctx, c, in) })

	add(s, &sdk.Tool{
		Name:        "remora_logs",
		Description: "Search the request log, or show one entry by id. Bodies are cut to 4096 bytes.",
		Annotations: readOnly,
	}, func(ctx context.Context, in LogsIn) (any, error) { return logs(ctx, c, in) })

	add(s, &sdk.Tool{
		Name:        "remora_stats",
		Description: "Request, token and cost totals over a range, optionally grouped by key, alias, provider or model.",
		Annotations: readOnly,
	}, func(ctx context.Context, in StatsIn) (any, error) {
		from, to, err := timeRange(in.From, in.To)
		if err != nil {
			return nil, err
		}
		return ok200(c.GetStatsWithResponse(ctx, &gen.GetStatsParams{
			From: from, To: to, GroupBy: (*gen.GetStatsParamsGroupBy)(opt(in.GroupBy)),
			Key: opt(in.Key), Alias: opt(in.Alias), Provider: opt(in.Provider),
		}))
	})

	add(s, &sdk.Tool{
		Name:        "remora_alias_compile",
		Description: "Preview the chain an alias definition compiles to, before saving it.",
		Annotations: readOnly,
	}, func(ctx context.Context, in gen.Alias) (any, error) {
		return ok200(c.CompileAliasWithResponse(ctx, in))
	})

	add(s, &sdk.Tool{
		Name:        "remora_provider_test",
		Description: "Test one provider. This sends one small real request to the provider.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: new(false), OpenWorldHint: new(true)},
	}, func(ctx context.Context, in ProviderTestIn) (any, error) {
		return ok200(c.TestProviderWithResponse(ctx, in.Name))
	})

	add(s, &sdk.Tool{
		Name:        "remora_config_export",
		Description: "The whole configuration as remora.yaml; secrets are env:NAME references.",
		Annotations: readOnly,
	}, func(ctx context.Context, _ struct{}) (ConfigExportOut, error) {
		// The raw call: the generated parser would decode the YAML body instead of keeping its text.
		resp, err := c.ExportConfig(ctx)
		if err != nil {
			return ConfigExportOut{}, err
		}
		defer func() { _ = resp.Body.Close() }()
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			return ConfigExportOut{}, err
		}
		if resp.StatusCode == http.StatusOK {
			return ConfigExportOut{Document: string(b)}, nil
		}
		var e gen.Error
		_ = json.Unmarshal(b, &e)
		_, err = check[string](resp.StatusCode, nil, &e, nil)
		return ConfigExportOut{}, err
	})
}

// opt is nil for "", else a pointer to s.
func opt(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func timeRange(from, to string) (*time.Time, *time.Time, error) {
	f, err := parseTime("from", from)
	if err != nil {
		return nil, nil, err
	}
	t, err := parseTime("to", to)
	return f, t, err
}

func parseTime(field, v string) (*time.Time, error) {
	if v == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return nil, fmt.Errorf("%s: %q is not an RFC 3339 time, such as 2026-10-16T09:00:00Z", field, v)
	}
	return &t, nil
}

func inspect(ctx context.Context, c gen.ClientWithResponsesInterface, in InspectIn) (any, error) {
	limit := cmp.Or(in.Limit, 50)
	offset := &in.Offset
	if in.Offset == 0 {
		offset = nil
	}
	q := opt(in.Q)
	switch in.Kind {
	case "providers":
		if in.ID != "" {
			return ok200(c.GetProviderWithResponse(ctx, in.ID))
		}
		return ok200(c.ListProvidersWithResponse(ctx, &gen.ListProvidersParams{Limit: &limit, Offset: offset, Q: q}))
	case "accounts":
		if in.ID != "" {
			return account(ctx, c, in.ID)
		}
		return ok200(c.ListAccountsWithResponse(ctx, &gen.ListAccountsParams{Limit: &limit, Offset: offset, Q: q}))
	case "aliases":
		if in.ID == "" {
			return ok200(c.ListAliasesWithResponse(ctx, &gen.ListAliasesParams{Limit: &limit, Offset: offset, Q: q}))
		}
		a, err := ok200(c.GetAliasWithResponse(ctx, in.ID))
		if err != nil {
			return nil, err
		}
		chain, err := ok200(c.GetAliasChainWithResponse(ctx, in.ID))
		if err != nil {
			return nil, err
		}
		return map[string]any{"alias": a, "chain": chain}, nil
	case "keys":
		if in.ID == "" {
			return ok200(c.ListKeysWithResponse(ctx, &gen.ListKeysParams{Limit: &limit, Offset: offset, Q: q}))
		}
		k, err := ok200(c.GetKeyWithResponse(ctx, in.ID))
		if err != nil {
			return nil, err
		}
		usage, err := ok200(c.GetKeyUsageWithResponse(ctx, in.ID, &gen.GetKeyUsageParams{}))
		if err != nil {
			return nil, err
		}
		return map[string]any{"key": k, "usage": usage}, nil
	case "tokens":
		if in.ID != "" {
			return nil, toolError("Tokens are listed only.")
		}
		return ok200(c.ListApiTokensWithResponse(ctx, &gen.ListApiTokensParams{Limit: &limit, Offset: offset, Q: q}))
	case "cooling":
		if in.ID != "" {
			return nil, toolError("Cooling is listed only.")
		}
		return ok200(c.ListCoolingWithResponse(ctx, &gen.ListCoolingParams{Limit: &limit, Offset: offset, Q: q}))
	case "quota":
		quota, err := ok200(c.GetQuotaWithResponse(ctx))
		if err != nil {
			return nil, err
		}
		if in.ID == "" {
			return map[string]any{"items": *quota}, nil // structuredContent must be an object
		}
		for _, p := range *quota {
			if p.Provider == in.ID {
				return p, nil
			}
		}
		return nil, toolError(fmt.Sprintf("No quota for %s: it has no accounts.", in.ID))
	case "account_strategies":
		if in.ID != "" {
			return nil, toolError("Account strategies are listed only.")
		}
		return ok200(c.ListAccountStrategiesWithResponse(ctx, &gen.ListAccountStrategiesParams{Limit: &limit, Offset: offset}))
	case "logins":
		if in.ID == "" {
			return nil, toolError("A login state is required.")
		}
		return ok200(c.GetAccountLoginWithResponse(ctx, in.ID))
	default:
		return nil, fmt.Errorf("kind must be one of %s", inspectKinds)
	}
}

// account finds one account by id; the API has no single-account read.
func account(ctx context.Context, c gen.ClientWithResponsesInterface, id string) (any, error) {
	limit := 200
	for offset := 0; ; offset += limit {
		page, err := ok200(c.ListAccountsWithResponse(ctx, &gen.ListAccountsParams{Limit: &limit, Offset: &offset}))
		if err != nil {
			return nil, err
		}
		for _, a := range page.Items {
			if a.Id == id {
				return a, nil
			}
		}
		if len(page.Items) == 0 || offset+len(page.Items) >= page.Total {
			return nil, toolError(fmt.Sprintf("No account %s.", id))
		}
	}
}

func logs(ctx context.Context, c gen.ClientWithResponsesInterface, in LogsIn) (any, error) {
	if in.ID != "" {
		l, err := ok200(c.GetLogWithResponse(ctx, in.ID))
		if err != nil {
			return nil, err
		}
		l.Request, l.Response = truncateBody(l.Request), truncateBody(l.Response)
		return l, nil
	}
	from, to, err := timeRange(in.From, in.To)
	if err != nil {
		return nil, err
	}
	limit := cmp.Or(in.Limit, 20)
	offset := &in.Offset
	if in.Offset == 0 {
		offset = nil
	}
	return ok200(c.ListLogsWithResponse(ctx, &gen.ListLogsParams{
		Limit: &limit, Offset: offset, From: from, To: to, Q: opt(in.Q), Key: opt(in.Key), Alias: opt(in.Alias),
		Provider: opt(in.Provider), Model: opt(in.Model), Status: (*gen.ListLogsParamsStatus)(opt(in.Status)),
	}))
}

// truncateBody replaces a body whose JSON exceeds maxBody bytes with {"truncated": true, "bytes": <full length>,
// "head": "<its first maxBody bytes>"}; a smaller body passes.
func truncateBody(m *map[string]any) *map[string]any {
	if m == nil {
		return nil
	}
	b, err := json.Marshal(*m)
	if err != nil || len(b) <= maxBody {
		return m
	}
	return &map[string]any{"truncated": true, "bytes": len(b), "head": string(b[:maxBody])}
}
