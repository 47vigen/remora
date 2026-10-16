# remora: account quota, routing strategies and an MCP server — design

Date: 2026-10-16. Status: accepted (owner delegated every decision; the calls made on his behalf are marked).
Research: [quota](../research/2026-10-16-quota.md), [routing](../research/2026-10-16-routing.md),
[MCP](../research/2026-10-16-mcp.md).

## Intent

Three features the owner needs to run a pool of about 20 subscription accounts in production and to hand the
gateway to an agent:

1. **Account quota.** Know, for every subscription account, how much of each quota window is left and when it
   resets, on one page, without guessing from cooldowns.
2. **Routing strategies.** Choose how load spreads, both across the members of an alias and across the accounts of
   one subscription provider: `priority`, `fill-first`, `round-robin`, `p2c`, `random`, `least-used`.
3. **MCP.** Let Claude Code, or any MCP client, operate all of remora: inspect, change routing, manage accounts,
   read logs and stats, check quota, plan and apply configuration.

The research measured each feature against OmniRoute and similar gateways. remora takes only what serves these three
jobs. OmniRoute's breadth is the anti-model: about 110 MCP tools, about 80 quota providers, quota pools, burn-rate
charts, and strategies that are near-duplicates of each other.

## Goals

- Exact quota numbers for the providers of the real pool (Antigravity, Codex, Claude), read with each account's own
  credential, at a gentle and bounded upstream cost.
- Six strategies with precise, documented semantics at both levels, built on the engines' own routing so cooling,
  retry, fallback and session affinity keep working.
- An MCP endpoint on the existing listener, authenticated by the existing API tokens, with a compact tool set that
  covers the management API.

## Non-goals

- Quota history, charts, burn rate, forecasts, alerts or notifications.
- Quota for Kimi, Grok and other providers (a later addition through the same fetcher interface). Qwen and iFlow
  have no known quota endpoint: they show cooldowns only.
- Using quota numbers to steer routing (the engine already cools an account on a quota error, with the reset time).
- Weighted strategies, cost-, latency- or quota-scored strategies, OmniRoute's `auto` and `fusion`.
- Session affinity at alias level (a conversation may move between alias members under a spreading strategy;
  account-level affinity is the engine's and stays on).
- MCP resources, prompts, sampling, elicitation, OAuth, per-tool scopes, a chat tool, tools generated from OpenAPI.
- Creating or deleting API tokens, or changing the admin password, through MCP.

## Anti-goals

- **Spending quota to measure quota.** No probe that sends an inference request.
- **Load on upstreams.** No burst at startup, no parallel polling, no polling faster than five minutes.
- **A number that lies.** An unknown value is shown as unknown, never as 0 % or 100 %.
- **Strategy names that mean nothing.** Every accepted strategy behaves as documented at that level; where two names
  behave the same, the documentation says so.
- **A fork that drifts.** The one engine patch below is small, recorded in `docs/patches.md`, and offered upstream.

## 1. Account quota

### Source

The OAuth engine's management API can send an HTTP request upstream as a chosen credential
(`POST /v8/management/requests/api-call`, `$TOKEN$` replaced by the credential's token, sent through the
credential's proxy). The engine refreshes the token first only for Antigravity (and Grok); for Codex and Claude it
sends the stored token, so a 401 there is answered by asking the engine to refresh the credential
(`POST /v8/management/credentials/refresh`) and retrying once. The engine's own console builds its quota page this way. remora adds one client method for it
and parses each provider's answer in Go. No engine change.

The engine also records passive quota signals from the response headers of real traffic, for Codex and Claude only
(`GET /credentials` → `quota.signals`). remora keeps them on its account model but the API adapter drops them. It
now parses them and uses them as a free
snapshot: an account whose passive snapshot is younger than the poll interval is not polled.

### Providers

| provider | call | windows |
|---|---|---|
| antigravity | `POST {base}/v1internal:retrieveUserQuotaSummary` `{"project": <project_id>}`, base `https://daily-cloudcode-pa.googleapis.com` then `https://cloudcode-pa.googleapis.com`; plan from `v1internal:loadCodeAssist` (once per account per day) | per model family (Gemini; Claude and GPT), each a 5-hour and a weekly window |
| codex | passive `x-codex-*` signals; else `GET https://chatgpt.com/backend-api/wham/usage` with `chatgpt-account-id` | windows labelled by their length (5 h, weekly), plus per-model additional limits |
| claude | passive `anthropic-ratelimit-unified-*` signals; else `GET https://api.anthropic.com/api/oauth/usage` with `anthropic-beta: oauth-2025-04-20` | `five_hour`, `seven_day`, `seven_day_opus`, `seven_day_sonnet` |

Each call sends the User-Agent the engine itself uses for that provider. For Codex and Claude remora keeps the
strings as constants, and a unit test reads them from the engine's source in `engines/cliproxyapi`, so an engine bump
that changes them fails the test. Antigravity refuses clients older than a minimum version, and the engine learns the
current client version at run time from a public version manifest (refreshed every 3 hours, with a floor constant in
its source). remora does the same (my call): it reads the same manifest at most every 3 hours, falls back to the same
floor, and a unit test asserts that the manifest URL and the floor match the engine's source. All endpoints are private and undocumented: every parser accepts missing fields and reports them as
unknown.

### Data

One snapshot per account, overwritten, persisted in the store (so a restart does not trigger a burst):

```
AccountQuota {
  account_id
  plan          string, "" = unknown
  windows       [{ scope, window, remaining (0..1 or null = unknown), resets_at (null = unknown or no reset) }]
                scope: a model family or model ("gemini", "claude-gpt", "gpt-5-codex", "" = whole account)
                window: "5h", "weekly", or the length in minutes when nothing else fits
  limit_reached bool (any window at 0, or the provider says so)
  source        "poll" | "passive"
  fetched_at
  error         "" | a user-facing message ("sign in again", "the provider refused the request", ...)
}
```

A provider without a fetcher has no snapshot; the page shows its cooldowns only.

### Fetching (bounded, my call on every number)

- One background worker for all accounts, strictly sequential, at least 2 s between upstream calls.
- Each account is due every `REMORA_QUOTA_INTERVAL` (default `15m`, refused below `5m`; `0` turns polling off and
  leaves on-demand refresh).
- Skipped: disabled accounts, accounts whose passive snapshot is fresh, and accounts with every window at 0 until
  the earliest reset time has passed (then polled once).
- At start, only accounts whose stored snapshot is older than the interval are due, and they go through the same
  spaced queue.
- On-demand refresh (one account, or all of one provider) goes through the same queue, with a 60 s floor per account.
- 429 or 5xx: the account's next poll backs off (doubling from the interval, capped at 4 h). 401/403: one retry
  after asking the engine to refresh the credential; after that the snapshot's error is "sign in again".
- About 20 Antigravity accounts at 15 minutes is about 80 upstream calls an hour.

### The page: Quota

A new console page, **Quota**, next to Accounts in the navigation.

- One section per provider that has accounts. Its header counts accounts that are available, exhausted, cooling and
  needing sign-in, shows the pool's earliest reset, and has a "Refresh all" action.
- One row per account: label, plan, one bar per window (remaining %, colour by level, reset countdown), the
  engine's current cooldowns, "updated N min ago" with its source, the error if any, and a refresh action.
- Providers without a fetcher show their accounts with status and cooldowns only, and say quota is not available
  for that provider.
- Dense, right sized, no empty cards; follows the console's existing table and chip components.

### API

- `GET /api/quota` → every account's snapshot merged with its status and cooldowns, grouped by provider.
- `POST /api/quota/refresh` `{account_id? , provider?}` → queues refreshes, answers 202 with what was queued and
  what was skipped by the floor.
- The existing `Account.quota` field in `GET /api/accounts` keeps its shape.

## 2. Routing strategies

### Semantics

The six names behave the same way at both levels wherever the engines allow it. "Live" means not cooling and not
disabled. One load signal is used everywhere: **requests started in the last 60 seconds**, kept in memory per target
(alias level: per provider model; account level: per credential and model). It is exact, cannot leak the way an
in-flight counter can when a completion is missed, and a member back from cooldown starts at zero recent load, not at
a lifetime total that floods it. (My call: one signal instead of in-flight for p2c and a window for least-used.)

| strategy | first pick | fallback after the first pick |
|---|---|---|
| `priority` | the first live member in the declared order (alias) / the highest priority tier, rotating inside a tier (account) | the rest, in order |
| `fill-first` | the first live member in order, staying on it until it cools | the rest, in order |
| `round-robin` | a rotation with one cursor per alias / per provider pool | the other live members, rotated from the pick |
| `random` | uniform over the live members | the other live members, rotated from the pick |
| `p2c` | two distinct live members at random; the one with fewer recent requests wins (ties: declared order) | the other live members, rotated from the pick |
| `least-used` | the live member with the fewest recent requests (ties: declared order) | the other live members, rotated from the pick |

At alias level `priority` and `fill-first` behave identically (cooling already moves a fill-first alias to the next
member). Both names are accepted and stored as written, and the documentation says they are the same at this level.
At account level they differ: `priority` honours the per-account priority numbers and rotates inside equal numbers;
`fill-first` uses one account at a time, in the order priority (high first), then oldest account first.

### Alias level: header-selected rules (no engine change)

The front door matches a routing rule per alias and keeps one fallback chain per rule; a matching rule overrides any
`fallbacks` in the request body. So remora's proxy cannot reorder a chain per request, but it can choose which rule
matches.

- `priority`, `fill-first`: unchanged, one rule (the compiled chain, cooling members filtered out).
- The four spreading strategies: one rule per live member, matching the alias **and** the internal header
  `X-Remora-First: <member ref>`; its target is that member and its fallbacks are the other live members rotated from
  it. The header-less rule stays as the default at a lower priority, so a request without a valid header still
  works.
- The proxy picks the member from the alias's live chain, sets the header, and removes any client-sent copy. Today
  the proxy reads the alias definition from the store and only the reconciler compiles and filters chains; the
  reconciler therefore publishes, after each successful apply, the live chain per spreading alias to an in-memory
  snapshot the proxy reads, so the proxy never names a member that has no rule. The header never reaches a provider (asserted by an e2e test against the fake
  upstream).
- The picked member is never repeated in its own chain, so the current `Spread` field and the fallback cap go away.
- The 60 s counters live in the proxy; a start is recorded against the member named by `x-remora-served-by` when the
  response arrives, so a fallback is counted where it landed.
- WebSocket `/v1/responses`: headers are fixed at the handshake, before any model is known, so a WebSocket session
  always uses the alias's default (declared-order) rule. This is documented next to the strategies; account-level
  strategies still apply to it.
- Rules grow to aliases × members for spreading aliases. The plan measures rule evaluation with `make bench` and
  must keep the chat c=10 p99 within run-to-run spread of the current numbers.

Migration: stored `round-robin` (today a uniform random pick) becomes `random`, which keeps every existing alias's
behaviour; stored "no strategy" becomes `priority`. The API, `remora.yaml` and the OmniRoute importer accept all six
names (OmniRoute uses the same names), and an omitted strategy means `priority`. CHANGELOG notes the change.

### Account level: per subscription provider

Each subscription provider (antigravity, codex, ...) has one strategy, default `round-robin` (today's behaviour), and
each account has a priority number, default 0.

- `priority`, `fill-first`, `round-robin` are compiled into the engine's native per-credential priority tiers through
  the management API remora already uses (`fill-first`: a distinct tier per account in the documented order;
  `round-robin`: one tier).
- `random`, `least-used`, `p2c` need a selector the engine does not have. The engine's scheduler-plugin API could
  choose the account, but a plugin is consulted before the session-affinity selector and would bypass affinity, and
  plugins are native code built against the exact engine build. So: **one engine patch** (my call, because the owner
  asked for all six at account level): on the fork
  `47vigen/CLIProxyAPI`, branch `remora/routing` on the pinned release tag,
  1. three selectors, `random`, `least-used` and `p2c`, using the same 60 s start counts inside the selector;
  2. `routing.provider-strategies`, a map from provider to strategy that overrides the global strategy for that
     provider, so each pool keeps its own strategy.
  The patch keeps the engine's tiers, cooldown and session affinity around the new selectors (affinity still wins
  for a conversation that has a key). It is recorded in `docs/patches.md` with `upstream PR: pending`; the upstream
  pull request is opened only with the owner's word.
- Session affinity (from the performance work) stays on for every strategy: the strategy decides only first turns
  and rebinds.

### Where it is set

- Alias strategy: the alias form (a select with the six names and a one-line description each), `remora.yaml`
  `aliases[].strategy`, the API.
- Account strategy: a per-provider control on the Accounts page; `remora.yaml` gains
  `account_strategies: {<provider>: <strategy>}`; API `GET /api/accounts/strategies`,
  `PUT /api/accounts/strategies/{provider}`.
- Account priority: an editable column on the Accounts page; `PATCH /api/accounts/{id}` gains `priority`. Accounts
  are not in `remora.yaml` (they hold credentials), so priorities are not either.

## 3. MCP server

### Transport and auth

- Streamable HTTP at `/mcp` on the existing listener, stateless, JSON responses, using the official
  `github.com/modelcontextprotocol/go-sdk`.
- Authenticated by the existing API tokens (`rmt_…`) as `Authorization: Bearer`, through the existing auth service.
  No cookies, no OAuth, no scopes.
- `remora mcp`: the same tools over stdio for a local agent, using the CLI's `REMORA_URL` and `REMORA_TOKEN`.
- Claude Code: `claude mcp add --transport http remora https://<host>/mcp --header "Authorization: Bearer rmt_..."`.
  The README documents it.

### How tools work

Every tool calls the management API through the generated client. For `/mcp` the client's transport invokes the API
handler in process and forwards the caller's `Authorization` header, so MCP goes through exactly the same
authentication, validation and errors as `/api`. Under stdio the same tools use a network client. No second code path
for any operation.

### Tools

| tool | kind | covers |
|---|---|---|
| `remora_overview` | read | health, version, counts, accounts out of the pool, cooling, quota summary per provider |
| `remora_inspect` | read | `kind` (providers, accounts, aliases, keys, tokens, cooling, quota, account_strategies, logins) and optional `id`; an alias includes its compiled chain, a key its usage |
| `remora_logs` | read | search with filters, or one entry by id; bodies truncated |
| `remora_stats` | read | range, group by key, alias or provider, filters |
| `remora_alias_compile` | read | preview an alias definition's compiled chain before saving |
| `remora_provider_test` | read | test one provider (sends one real request; says so in its description) |
| `remora_config_export` | read | the current `remora.yaml` |
| `remora_config_plan` | read | a document (whole or partial), secrets, `prune` (default false) → the change list and a `plan_hash` |
| `remora_config_apply` | write, destructive | the same inputs and the `plan_hash`; refuses and returns the fresh plan if the hash no longer matches |

`plan_hash` lives in the management API, not in the MCP layer: `POST /api/config/plan` returns it, and
`POST /api/config/apply` accepts it optionally and answers 409 with the fresh plan when the store changed since. The
check and the apply run in the same transaction.
| `remora_delete` | write, destructive | a provider, alias, key or account |
| `remora_account` | write | enable, disable, set priority, reset cooldown, refresh quota, start, complete or cancel a sign-in, import a credential |

Every tool carries `readOnlyHint` / `destructiveHint` annotations. Providers, aliases, keys, alias strategies and
account strategies are written only through plan and apply: a partial document with `prune: false` adds or updates
exactly what it names and deletes nothing, so editing one alias is a short document plus a plan and an apply. A full
replace must say `prune: true`, and the plan's delete rows are what the user confirms.

## Delivery

Three plans, one branch and pull request each. 11 and 12 run in parallel; 13 starts after both are merged, because
its tools cover their endpoints:

| plan | delivers |
|---|---|
| 11 quota | the api-call client method, passive signal parsing, the three fetchers, the worker, the store table, the API, the Quota page |
| 12 routing | alias header rules and counters, the migration, account strategies and priorities, the engine fork patch, the UI controls, `remora.yaml` and importer |
| 13 mcp | `/mcp`, `remora mcp`, the tools, README section |

Each plan passes the repository's gates: `make lint test`, then `make image fakes-image e2e`, and `make bench` for
plan 12. The fake upstream gains a quota endpoint per provider so the fetchers are tested end to end without any real
upstream.

## Left to the builder

- Exact Go package boundaries (expected: `internal/quota`, `internal/mcp`, strategy picking in `internal/proxy`).
- Colours and thresholds of the quota bars, within the console's design tokens.
- The order of steps inside each plan.
