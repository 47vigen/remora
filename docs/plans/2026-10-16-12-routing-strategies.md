# Routing Strategies Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Six routing strategies — `priority`, `fill-first`, `round-robin`, `random`, `p2c`, `least-used` — with the
spec's exact semantics, at alias level (which member a request tries first) and at account level (which account of a
subscription provider serves it), on top of the engines' own cooling, fallback and session affinity.

**Architecture:** Alias level, no engine change: for a spreading alias the reconciler compiles one front-door rule
per live member, selected by the internal header `X-Remora-First`, with the other live members rotated from it as
fallbacks, plus the default rule; after each committed pass it publishes the live chains to a `proxy.Picker`, which
picks from 60 s start counts, sets the header (dropping any client copy) and counts by `x-remora-served-by`. Account
level: a per-provider strategy and per-account priority in the store; `priority`, `fill-first`, `round-robin` compile
into the OAuth engine's credential priorities, `random`, `least-used`, `p2c` need one patch on the fork
`47vigen/CLIProxyAPI` (three selectors plus `routing.provider-strategies`).

**Tech Stack:** Go 1.27, `net/http`, `math/rand/v2`, `sync/atomic`, SQLite migrations, oapi-codegen, Bun + Vite +
HeroUI v3 (`web/`), Docker Compose e2e with the fakes image, `tools/bench`.

**Spec:** `docs/specs/2026-10-16-quota-routing-mcp-design.md`, section "2. Routing strategies" plus Goals, Non-goals,
Anti-goals and Delivery (plan 12). Evidence: `docs/research/2026-10-16-routing.md`.

Engine versions verified: Bifrost fork `d3ad3068` on `transports/v2.3.0`, CLIProxyAPI `v8.0.31` (`355fd10e`), both
read from `lab/.worktree/perf-upstream/remora/engines/`. `B/` = `engines/bifrost`, `C/` = `engines/cliproxyapi`.

## Global Constraints

- `engines/` changes only by moving a pin. The OAuth engine change is commits on the fork branch `remora/routing` with
  a `docs/patches.md` row (plan 04 Task 8 is the procedure); the upstream PR waits for the owner's word (`pending`).
- The six names exactly, at both levels, everywhere (API, `remora.yaml`, importer, UI). Omitted alias strategy =
  `priority`; unset account strategy = `round-robin`.
- Semantics are the spec's table: first pick and fallback per strategy; "live" = not cooling and not disabled; ties
  go to declared order. Alias level: `priority` ≡ `fill-first`, documented. Account level: `priority` honours the
  numbers and rotates inside equal ones; `fill-first` uses one account at a time, priority high first, then oldest.
- One load signal: requests started in the last 60 seconds, in memory, per member ref (alias) or per credential and
  model (account). No in-flight counter, no lifetime count, nothing persisted.
- `X-Remora-First` never reaches a provider and no client can set it, on any `/v1` route or WebSocket handshake.
- No alias-level session affinity; the OAuth engine's account affinity stays on for every strategy.
- Every OAuth engine key goes in both `cliproxyConfig` (boot) and `settingsPatch` (runtime) and their shared test table.
- No engine name, engine provider id (`xai`) or internal provider in a user-facing string.
- Tests before code; `go test -race ./...` and `golangci-lint run` green after every task; a task touching
  `api/openapi.yaml` or `web/` also runs `cd web && bun run gen && bun run typecheck && bun run lint && bun run test`.
- Conventional Commits with scopes; bodies say why and cite the engine lines relied on.
- Branch `routing-strategies`, one PR to `main`. Plan 11 (quota) runs in parallel and also adds a migration and edits
  `internal/accounts`, `api/openapi.yaml` and the Accounts page: whichever merges second renumbers and rebases.

## Review Focus

1. **A client sending its own `X-Remora-First`** (HTTP or WebSocket handshake, live or cooling member): removed, the
   proxy's pick wins, a stale value falls to the default rule. Tests: Task 3 `TestRewriteReplacesClientFirstHeader`,
   `TestWSHandshakeDropsFirstHeader`; Task 2 `TestDefaultRuleCELExcludesOnlyLiveMembers`.
2. **A nested alias whose flat chain repeats a target**: two rules with one header value would shadow each other; the
   live chain is de-duplicated, first occurrence wins. Test: Task 2 `TestSync_SpreadingAliasDeduplicatesChain`.
3. **One live member, or every member cooling**: one → no member rules, nothing published, no header; all → `Filter`
   keeps the whole chain and every member keeps a rule. Tests: Task 2 `TestSync_SpreadingAliasOneLiveMember`,
   `TestSync_SpreadingAliasAllCooling`.
4. **`PATCH /api/accounts/{id}` with only `priority`, only `enabled`, or neither**: a priority edit never toggles the
   account; an empty body is a 400. Test: Task 8 `TestUpdateAccountPartial`.
5. **`fill-first` accounts with equal priority and creation time** (a bulk import): the order stays total and stable,
   or the engine's tiers flap every pass. Test: Task 7 `TestNativePriorities` (tie: lower id first).

## Engine facts this plan relies on

- Front-door rules run in ascending priority (`B/plugins/routing/rules/store.go:95,144,253`); the first match wins
  with one fallback chain for whichever target it picked (`engine.go:215-311`), replacing body fallbacks
  (`B/plugins/routing/main.go:544-560`). CEL sees lowercased request headers as `headers` (`engine.go:481-490`); a
  missing key is an evaluation error and the rule is skipped, so header tests are guarded with `"x-remora-first" in headers`.
- The reconciler keeps a rule's priority and gives a new rule the lowest free one ≥ `RulePriorityBase`
  (`internal/reconcile/reconcile.go:54-64,496-590`), so it cannot put a new member rule before an existing default
  rule. **Decision:** the spec's "default rule at a lower priority" is realised by the default rule's CEL excluding
  exactly the live member values: one alias's rules are mutually exclusive, order stays irrelevant, and a header
  naming no live member reaches the default rule.
- Non-forwarding of the header upstream is unverified (research, open risk); Task 4's e2e proves it.
- OAuth engine: `Selector.Pick(ctx, provider, model, opts, auths)` (`C/sdk/cliproxy/auth/conductor.go:102-104`);
  `RoundRobinSelector.Pick` takes the top available tier via `getSelectorAvailableAuths` and keys rotation
  `provider:canonicalModelKey(model)` (`selector.go:614-637`); priority = `Attributes["priority"]`, highest tier wins
  (`:364-377,469-585`), set by `PATCH /credentials/fields` (`C/internal/api/handlers/management/auth_files_fields.go:621,689-710`),
  listed by `GET /credentials` (`auth_files.go:746-761`, absent = 0).
- The routing state is compared with `!=` for a selector swap (`C/sdk/cliproxy/service_config.go:57-63,259-262`), so it
  stays comparable; `newRoutingSelector` wraps affinity around the strategy (`:100-125`); a swap drops selector state.
  Only builtin selectors take the scheduler fast path (`conductor_selection.go:67-73`); every pick passes `pickNext`
  (`:2083`) or `pickNextMixed` (`:2269`).
- `scripts/check-engine-pins.sh` reads the first `docs/patches.md` row per engine; its base tag must equal
  `engines/versions.env`, so `CLIPROXYAPI_VERSION` stays `v8.0.31`.

---

## Before Task 1: bench baseline

- [ ] On the branch's base commit: `make image fakes-image && make bench && make bench`. Keep both
  `bench-results/*.txt` (gitignored) and note the two `chat c=10` p99 values, `b1` and `b2`. Task 10 compares
  against them.

---

### Task 1: Six strategy names, migration and the edges that accept them

**Files:**
- Modify: `internal/alias/alias.go`, `internal/store/aliases.go` (`PutAlias`), `internal/api/aliases.go`,
  `api/openapi.yaml`, `internal/config/{file,plan,export}.go`, both `remora.schema.json` copies (same bytes),
  `internal/importer/omniroute/convert.go`, `internal/reconcile/desired.go` (Step 3); regenerate `internal/api/gen`
  (`go generate ./internal/api/gen`) and `web/src/client` (`cd web && bun run gen`)
- Create: `internal/store/migrations/0007_alias_strategy_names.sql`
- Test: `internal/alias/alias_test.go` (new), `internal/store/store_test.go`, `internal/api/aliases_test.go`,
  `internal/config/{file,service}_test.go`, `internal/importer/omniroute/convert_test.go`

**Interfaces:**
- Produces (in `internal/alias`):

```go
const (
    StrategyPriority   Strategy = "priority"
    StrategyFillFirst  Strategy = "fill-first"
    StrategyRoundRobin Strategy = "round-robin"
    StrategyRandom     Strategy = "random"
    StrategyP2C        Strategy = "p2c"
    StrategyLeastUsed  Strategy = "least-used"
)
var Strategies = []Strategy{StrategyPriority, StrategyFillFirst, StrategyRoundRobin, StrategyRandom, StrategyP2C, StrategyLeastUsed}
func (s Strategy) Valid() bool   // one of Strategies; "" is not valid (callers default it first)
func (s Strategy) Spreads() bool // round-robin, random, p2c, least-used

// FirstHeader names the member a spreading alias's request tries first; set only by the proxy (Task 3).
const FirstHeader = "X-Remora-First"

// Chain is the live chain of one spreading alias as the reconciler last pushed it: distinct member refs
// "<provider>/<model>" (user-facing, before ToEngineTarget), declared order, each with its own rule.
type Chain struct {
    Strategy Strategy
    Members  []string
}
```

`StrategyFallback` goes. Defaulting is `cmp.Or(s, alias.StrategyPriority)` in `PutAlias`, `toAlias`, `fromGenAlias`
and the importer; `Export` writes `""` for `priority`. OpenAPI gains `RoutingStrategy` (enum of the six, one line
each; Task 8 reuses it), `Alias.strategy: $ref RoutingStrategy`, always set on output. Copy: API
`strategy must be one of priority, fill-first, round-robin, random, p2c, least-used; got "<value>"`; file
`<path>.strategy: "<value>" is not one of priority, fill-first, round-robin, random, p2c, least-used`.

Migration `0007_alias_strategy_names.sql`:

```sql
-- round-robin was a uniform random pick: keep that behaviour under its new name. '' was ordered fallback.
UPDATE aliases SET strategy = 'random' WHERE strategy = 'round-robin';
UPDATE aliases SET strategy = 'priority' WHERE strategy = '';
```

- [ ] **Step 1: Write the failing tests.**

```go
func TestStrategyNames(t *testing.T) // Valid: six true; "", "fallback", "Round-Robin" false. Spreads: the four only
func TestMigrationRenamesStrategies(t *testing.T) // db built from 0001..0006 (user_version 5), aliases a 'round-robin',
// b '' → Open → a "random", b "priority", user_version == file count. TestOpenAppliesMigrations expects 6
func TestPutAliasDefaultsStrategy(t *testing.T) // PutAlias with Strategy "" → Aliases() reads "priority"
func TestFromGenAliasStrategy(t *testing.T) // absent → priority; six → as written; "fallback" → 400 copy; output always has strategy
func TestValidateStrategy(t *testing.T)    // file: omitted ok, six ok, "fallback" → error naming aliases[0].strategy
func TestPlanOmittedStrategyMatchesPriority(t *testing.T) // stored priority vs omitted → no change; random vs round-robin → update
func TestExportOmitsPriority(t *testing.T) // priority → strategy ""; p2c → "p2c"
// convert_test.go table: "priority"/"" → Strategy ""; each of the other five → kept as written, no warning;
// "strict-random" → "" with warning "alias x: strategy strict-random becomes priority"
```

- [ ] **Step 2: Run** `go test -race ./internal/alias/ ./internal/store/ ./internal/api/ ./internal/config/ ./internal/importer/...`.
  Expected: FAIL (`undefined: alias.StrategyPriority`, count 5 != 6).
- [ ] **Step 3: Implement.** In `desired.go` the `StrategyRoundRobin` case becomes `Strategy.Spreads()` until Task 2
  replaces it. The schema's `strategy` becomes the six-name enum.
- [ ] **Step 4: Run** the same command, plus `cd web && bun run gen && bun run typecheck && bun run test`.
  Expected: PASS.
- [ ] **Step 5: Commit** `feat(alias)!: six routing strategy names; stored round-robin becomes random` with the
  footer `BREAKING CHANGE: a remora.yaml or API "strategy: round-robin" now rotates through the live members; aliases
  stored by earlier versions were migrated to "random", which keeps their behaviour.` (release-please writes the
  CHANGELOG from it; do not edit `CHANGELOG.md`).

### Task 2: Header-selected rules in the reconciler

**Files:**
- Modify: `internal/reconcile/desired.go` (`aliasRule` → `aliasRules`, delete `maxFallbacks`),
  `internal/reconcile/reconcile.go` (`Publish`, `RulePriorityBase` comment), `internal/engine/bifrost/rules.go`
- Test: `internal/reconcile/reconcile_test.go` (replace `TestSync_RoundRobinAliasSpreadsTheFirstTry` and
  `TestSync_RoundRobinFallbacksCapped`), `internal/engine/bifrost/client_test.go`

**Interfaces:**
- Consumes: Task 1's `Strategy.Spreads`, `FirstHeader`, `Chain`.
- Produces:

```go
func MemberRuleName(alias string, n int) string // RuleName(alias) + "#" + strconv.Itoa(n), n from 1; "#" is not in an alias name
// MemberRuleCEL: RuleCEL(alias) + ` && "x-remora-first" in headers && headers["x-remora-first"] == ` + strconv.Quote(ref)
func MemberRuleCEL(alias, ref string) string
// DefaultRuleCEL: RuleCEL(alias) + ` && !("x-remora-first" in headers && headers["x-remora-first"] in [` +
// quoted refs joined by ", " + `])`
func DefaultRuleCEL(alias string, refs []string) string

// aliasRules validates and compiles like aliasRule. Not spreading, or under two distinct live members: one rule as
// today, chain nil. Spreading: the default rule (RuleName, DefaultRuleCEL, declared order) plus, for live member i,
// MemberRuleName(name, i) / MemberRuleCEL(name, ref_i), target m_i, fallbacks m_i+1..m_n, m_1..m_i-1. Only the
// default rule sets desiredRule.Alias (it owns the stored rule id); member rules use Object ruleObject(name)+"#"+i.
func (r *Reconciler) aliasRules(all map[string]alias.Alias, name string, isTarget, fallbackOK func(string) bool,
    reserved map[string]bool) ([]desiredRule, *alias.Chain, error)

// Reconciler field. Publish, when set, receives the chains of every spreading alias after a pass commits: after
// Store.Write returns nil in sync and in Change (never inside Batch's nested calls, never after a rollback).
Publish func(chains map[string]alias.Chain)
```

`desiredState` gains `Chains`, kept on the pass. `RoutingRule.Spread` goes: `ruleWire` sends one target, weight 1;
`toRule` keeps the first target (an old spread rule differs by MAC and is updated). Header values are user-facing
refs; targets and fallbacks still go through `ToEngineTarget`.

- [ ] **Step 1: Write the failing tests.**

```go
func TestSync_SpreadingAliasRulePerMember(t *testing.T)
// spread (random) = [up-a/m1 up-b/m1 up-c/m1] → 4 rules: default (DefaultRuleCEL of the 3 refs) target a, fallbacks
// [b c]; MemberRuleName("spread", 2) (MemberRuleCEL "up-b/m1") target b, fallbacks [c a]; no target in its own
// fallbacks; priorities distinct
func TestSync_SpreadingAliasDeduplicatesChain(t *testing.T) // outer = [inner, up-a/m1], inner = [up-a/m1, up-b/m1] → 2 member rules
func TestSync_SpreadingAliasOneLiveMember(t *testing.T)     // Filter keeps only up-a → one rule, Publish map has no "spread"
func TestSync_SpreadingAliasAllCooling(t *testing.T)        // Filter drops all → whole chain kept → 3 member rules
func TestSync_CoolingMemberLosesItsRule(t *testing.T)       // drop up-b → #3 deleted, #2 now targets up-c, chain [up-a/m1 up-c/m1]
func TestSync_PriorityAndFillFirstOneRule(t *testing.T)     // either strategy → only RuleName rule; not published
func TestSync_StrategyChangeRemovesMemberRules(t *testing.T) // random → priority: the #n rules are deleted, engine_sync rows gone
func TestSync_SubscriptionMemberRule(t *testing.T)          // member codex/m: header ref "codex/m", target ToEngineTarget({codex m})
func TestPublishOnlyAfterCommit(t *testing.T) // Sync → once with {"spread": {random, 3 refs}}; failed Change → never; Batch of two → once, after
func TestDefaultRuleCELExcludesOnlyLiveMembers(t *testing.T) // exact strings for two refs, one with a quote in the model
func TestRuleWireSingleTarget(t *testing.T)                  // bifrost: one target, weight 1
```

- [ ] **Step 2: Run** `go test -race ./internal/reconcile/ ./internal/engine/bifrost/`. Expected: FAIL
  (`undefined: MemberRuleName`).
- [ ] **Step 3: Implement** as specified; `pushRules` already records ids only for `dr.Alias != ""`.
- [ ] **Step 4: Run** the same command. Expected: PASS.
- [ ] **Step 5: Commit** `feat(reconcile): one header-selected rule per live member of a spreading alias` (body: one
  chain per rule, the exclusion decision, `Spread` and the cap gone).

### Task 3: The proxy's pick and the 60 s counters

**Files:**
- Create: `internal/proxy/recent.go`, `internal/proxy/pick.go`
- Modify: `internal/proxy/proxy.go`, `internal/proxy/ws.go` (comment), `internal/proxy/bench_test.go`,
  `cmd/remora/serve.go`
- Test: `internal/proxy/{recent,pick,proxy,ws}_test.go`

**Interfaces:**
- Consumes: Task 1's `alias.Chain`, `alias.FirstHeader`; Task 2's `Reconciler.Publish`.
- Produces:

```go
const recentWindow = 60 * time.Second

// recentStarts: 60 one-second buckets per key, a bucket reset when its second comes round. Concurrency-safe.
type recentStarts struct{ /* mu, now, per-key [60]struct{sec int64; n int} */ }
func newRecentStarts(now func() time.Time) *recentStarts
func (r *recentStarts) Add(key string)
func (r *recentStarts) Count(key string) int

// Picker: chains in an atomic.Pointer, one atomic cursor per alias, a recentStarts. nil now/intn = time.Now/rand.IntN.
type Picker struct{ /* ... */ }
func NewPicker(now func() time.Time, intn func(n int) int) *Picker
func (p *Picker) SetChains(chains map[string]alias.Chain) // replaces the whole map; Reconciler.Publish
// Pick: the ref to send as X-Remora-First, "" without a published chain. round-robin: cursor mod len; random:
// intn(len); p2c: two distinct draws, fewer starts wins; least-used: fewest starts. Ties → lower index.
func (p *Picker) Pick(aliasName string) string
func (p *Picker) Started(servedBy string) // "" ignored
func (p *Picker) starts(ref string) int   // for tests
```

Wiring: `forward` sets `info.first = p.Picker.Pick(model)` for an alias when `p.Picker != nil`.
`setUpstreamHeaders` deletes `x-remora-first` (covering the WebSocket handshake too); `rewrite` then sets
`alias.FirstHeader` when `info.first != ""`. `modifyResponse` calls `Started(resp.Header.Get(headerServedBy))` after
the hooks, beside `setDroppedParams`, so the OAuth rename has run; every forwarded response counts, alias or direct.
`serve.go`: `picker := proxy.NewPicker(nil, nil)`, `rec.Publish = picker.SetChains` before the first `rec.SyncAll`,
`px.Picker = picker`. The `serveResponsesWS` comment: a WebSocket session always uses the default (declared-order)
rule, because headers are fixed at the handshake before any model is known.

- [ ] **Step 1: Write the failing tests.**

```go
func TestRecentStartsWindow(t *testing.T) // t0: Add ×3, t0+30s: Add → 4 at t0+59s, 1 at t0+61s, 0 at t0+91s; other key 0
func TestPickRoundRobin(t *testing.T)  // chain [a b c] → a, b, c, a; a second alias has its own cursor
func TestPickRandom(t *testing.T)      // intn stub 2 → c; picks are only ever members
func TestPickP2C(t *testing.T)         // starts a=3 b=1 c=0, intn yields 0 then 1 → b; yields 0 then 0 then 2 → c
                                       // (second draw redrawn until distinct); a=b=0 drawn (1,0) → a
func TestPickLeastUsed(t *testing.T)   // a=2 b=0 c=0 → b; all zero → a
func TestPickWithoutChain(t *testing.T) // unknown alias, or chains without it → ""
func TestSetChainsShrinks(t *testing.T) // [a b c] cursor at c, SetChains [a b] → never c
func TestPickConcurrent(t *testing.T)   // 8 goroutines × 1000 Pick+Started under -race; counts sum to 8000
func TestRewriteReplacesClientFirstHeader(t *testing.T) // client "X-Remora-First: up-c/m1" (any case): spreading alias →
// upstream sees only the pick; direct provider/model or priority alias → no header at all
func TestWSHandshakeDropsFirstHeader(t *testing.T) // client handshake header never reaches the upstream dial
func TestStartedCountsServedBy(t *testing.T) // pick up-a/m1, served-by up-b/m1 → b 1, a 0; "remora-oauth/codex/m"
// through ServedByHook(stub → "codex/m") → starts("codex/m") 1
func BenchmarkPickP2C(b *testing.B) // 4 members, Pick + Started per op; recorded in the commit body
```

- [ ] **Step 2: Run** `go test -race ./internal/proxy/ -run 'Recent|Pick|FirstHeader|Started'`. Expected: FAIL
  (`undefined: NewPicker`).
- [ ] **Step 3: Implement** as specified.
- [ ] **Step 4: Run** `go test -race ./internal/proxy/ ./cmd/... && go test -run '^$' -bench 'PickP2C|RequestPath' ./internal/proxy/`.
  Expected: PASS; `BenchmarkPickP2C` well under 1 µs/op and 0 allocs/op on a cached alias.
- [ ] **Step 5: Commit** `feat(proxy): pick a spreading alias's first member from 60 s start counts`.

### Task 4: Alias-level e2e, bench scenario and docs

**Files:**
- Create: `test/e2e/strategy_test.go` (`//go:build e2e`)
- Modify: `test/e2e/testdata/remora.yaml`, `tools/bench/remora.yaml`, `tools/bench/run.sh`, `README.md`

**Interfaces:**
- Consumes: `chat`, `chatBody`, `once`, `received`, `resetFakes`, `wantHeader` (`test/e2e`). Each test uses its own
  model names so the 60 s counters of one test never touch another's (the fake answers any model).

`test/e2e/testdata/remora.yaml` gains:

```yaml
  - {name: spread-rr, kind: chat, members: [up-a/rr, up-b/rr, up-c/rr], strategy: round-robin}
  - {name: spread-lu, kind: chat, members: [up-a/lu, up-b/lu, up-c/lu], strategy: least-used}
  - {name: spread-fb, kind: chat, members: [up-a/fb, up-b/fb], strategy: least-used}
```

- [ ] **Step 1: Write the tests.**

```go
func TestFirstHeaderNeverReachesUpstream(t *testing.T) // 3 × spread-rr with client "X-Remora-First: up-c/rr" → served-by
// {up-a/rr, up-b/rr, up-c/rr} (no pin); no request recorded on a, b, c carries the header in any case
func TestRoundRobinSpreads(t *testing.T)   // 6 × spread-rr → len(received) == 2 on each of a, b, c
func TestLeastUsedSpreads(t *testing.T)    // 3 × spread-lu, sequential → served-by up-a/lu, up-b/lu, up-c/lu in order
func TestFallbackAfterPick(t *testing.T) // once a: 429 rate_limit_exceeded, no Retry-After (e2e: REMORA_COOLING_429_COUNT=1000)
// → served-by up-b/fb, x-remora-fallback "true"; next request → up-a/fb (the start counted where it landed)
```

- [ ] **Step 2: Run** `make image fakes-image && go test -tags e2e -count=1 ./test/e2e/ -run 'FirstHeader|Spreads|FallbackAfterPick'`.
  Expected: PASS. Prove the header test bites: drop the `x-remora-first` case, rebuild, rerun: FAIL; restore.
- [ ] **Step 3: Bench scenario.** `tools/bench/remora.yaml` gains `spread-01`..`spread-10`, each `strategy: p2c` over
  `[up/m1, up/m2, up/m3, up/m4]` (50 rules, a pool-sized table every request walks). `run.sh` adds after the
  `chat c=$c` loop: `reset; run "chat spread c=10" -url "$remora/v1/chat/completions" -model spread-01 -c 10`.
  Existing labels stay, so files compare line by line.
- [ ] **Step 4: Docs.** README `## Routing strategies` after "Thinking level": the spec's table and paragraph for both
  levels; "live"; the 60-second signal (resets on restart); `priority` ≡ `fill-first` for an alias; account settings
  on the Accounts page and in `account_strategies`; WebSocket sessions use declared order; a conversation may move
  between alias members; old `round-robin` aliases now read `random`. No engine name.
- [ ] **Step 5: Commit** `test(e2e): spreading strategies spread, fall back and never leak the pick header`,
  `perf(bench): a spreading-alias scenario over a pool-sized rule table`, `docs: routing strategies`.

### Task 5: Engine patch on the fork

**Files (in `engines/cliproxyapi` on the fork branch; this repo gets only the pin):**
- Create: `sdk/cliproxy/auth/selector_spread.go` and its `_test.go`
- Modify: `internal/config/config_types.go`, `sdk/cliproxy/service_config.go`, `sdk/cliproxy/auth/conductor_selection.go`,
  `config.example.yaml` (comments)
- Test: `sdk/cliproxy/service_config_test.go`, the `internal/config` load test

**Interfaces:**
- Produces:

```go
// internal/config: RoutingConfig gains
ProviderStrategies map[string]string `yaml:"provider-strategies,omitempty" json:"provider-strategies,omitempty"`

// sdk/cliproxy/auth
const recentStartWindow = 60 * time.Second
func RecordStart(authID, model string)                     // package level, survives selector swaps
func recordStartAt(authID, model string, at time.Time)     // tests
func recentStarts(authID, model string, now time.Time) int // key authID + ":" + canonicalModelKey(model)
type RandomSelector struct{ intn func(int) int }           // uniform
type LeastUsedSelector struct{}                            // fewest starts; tie → candidate (ID) order
type P2CSelector struct{ intn func(int) int }              // two distinct draws, fewer starts; tie → ID order
// ProviderStrategySelector dispatches on Pick's provider; others use Default.
type ProviderStrategySelector struct {
    Default    Selector
    ByProvider map[string]Selector
}
func SelectorForStrategy(name string) (Selector, bool) // round-robin, fill-first, weighted-round-robin (and their
                                                       // existing aliases), random, least-used, p2c
```

The new selectors start like `RoundRobinSelector.Pick` (`getSelectorAvailableAuths`, then
`preferCodexWebsocketAuths`), so tiers and cooldown behave as for the builtins. `pickNext` and `pickNextMixed` call
`RecordStart` for the auth they return, so affinity-bound picks count too. `routingRuntimeState` gains
`providerStrategies string`: sorted `provider=strategy` pairs, comma-joined, dropping entries equal to the global
strategy and unknown names, so the struct stays comparable and an all-default map keeps today's selector and fast
path. The global strategy also accepts the three new names. `newRoutingSelector` builds `ProviderStrategySelector`
when `providerStrategies != ""` and wraps affinity around it as today: a binding still wins; the strategy decides
first turns and rebinds.

- [ ] **Step 1: Branch.** In `engines/cliproxyapi`: `git fetch https://github.com/47vigen/CLIProxyAPI.git remora/routing`,
  `git switch -c remora/routing FETCH_HEAD`, then `git merge-base --is-ancestor 355fd10e HEAD` (exit 0).
- [ ] **Step 2: Write the failing tests.**

```go
func TestRecentStartsExpireAfterWindow(t *testing.T)       // recordStartAt ×2 at t0 → 2 at t0+59s, 0 at t0+61s; keyed per model
func TestRandomSelectorUsesTopTierOnly(t *testing.T)       // auths p=1 {a,b}, p=0 {c}; intn stub over 200 picks → only a, b, both seen
func TestLeastUsedSelectorFewestRecentStarts(t *testing.T) // starts a=2 b=0 c=1 → b; all equal → first by ID
func TestP2CSelectorLowerOfTwo(t *testing.T)               // starts a=5 b=1 c=0, draws (0,1) → b; draws (0,0,2) → c
func TestSpreadSelectorsSkipCoolingAuths(t *testing.T)     // a cooling for model m → never picked by any of the three
func TestProviderStrategySelectorDispatch(t *testing.T)    // codex→LeastUsed, default RoundRobin: codex picks by counts, xai rotates
func TestSessionAffinityWinsOverSpreadSelectors(t *testing.T) // affinity-wrapped P2C: a bound session keeps its auth while live
func TestRoutingStateProviderStrategies(t *testing.T) // {codex:p2c, xai:round-robin} → "codex=p2c"; {xai:round-robin} → ""
// and today's selector types; a map change changes the state; "bogus" dropped
func TestConfigLoadsProviderStrategies(t *testing.T)        // YAML routing.provider-strategies round-trips through Load
```

- [ ] **Step 3: Run** `cd engines/cliproxyapi && go test ./sdk/cliproxy/auth/ ./sdk/cliproxy/ ./internal/config/ -run 'RecentStarts|Selector|ProviderStrateg|SpreadSelectors'`.
  Expected: FAIL (undefined names).
- [ ] **Step 4: Implement** as specified.
- [ ] **Step 5: Run** `cd engines/cliproxyapi && go vet ./sdk/... ./internal/config/... && go test ./sdk/cliproxy/... ./internal/config/... ./internal/api/handlers/management/...`.
  Expected: `ok` for every package.
- [ ] **Step 6: Commit on the fork and push:** `feat(auth): random, least-used and p2c credential selectors` and
  `feat(config): routing.provider-strategies overrides the strategy per provider`, then
  `git -C engines/cliproxyapi push https://github.com/47vigen/CLIProxyAPI.git remora/routing`. Record the tip commit.
- [ ] **Step 7: Upstream PR** — ask the owner before this outward step; until he says so it stays `pending`.

### Task 6: Pin the fork, record the patch

**Files:**
- Modify: `.gitmodules`, `engines/cliproxyapi` (submodule pointer), `docs/patches.md`
- Test: `internal/engine/cliproxyapi/contract_test.go` (the `engine`-tagged file)

- [ ] **Step 1: Point the submodule at the fork.** `git config -f .gitmodules submodule.engines/cliproxyapi.url
  https://github.com/47vigen/CLIProxyAPI.git`, `git submodule sync engines/cliproxyapi`,
  `git -C engines/cliproxyapi checkout <tip>`; `engines/versions.env` unchanged.
- [ ] **Step 2: `docs/patches.md` row** for `cliproxyapi`: files `sdk/cliproxy/auth/selector_spread.go`,
  `sdk/cliproxy/service_config.go`, `internal/config/config_types.go`; change "`random`, `least-used`, `p2c` selectors
  on 60 s start counts; `routing.provider-strategies`"; why "per-provider account strategies"; fork commit `<tip>`;
  upstream PR `pending`; base tag `v8.0.31`; remove when "the PR is in a release tag".
- [ ] **Step 3: Run** `make check-pins`. Expected: `ok cliproxyapi fork <tip> on v8.0.31`; bifrost line unchanged.
- [ ] **Step 4: Write the engine test.**

```go
func TestEngineProviderStrategiesRoundTrip(t *testing.T) // real binary: PatchConfig routing.provider-strategies {codex: p2c}
// → nil; Config() shows it; /healthz still answers
```

- [ ] **Step 5: Run** `cd engines/cliproxyapi && CGO_ENABLED=1 go build -o /tmp/cpa ./cmd/server && cd - &&
  REMORA_CLIPROXYAPI_BIN=/tmp/cpa go test -race -tags engine ./internal/engine/cliproxyapi/`. Expected: PASS.
- [ ] **Step 6: Commit** `build(engines): run the OAuth engine from the routing fork`. Body: the patch, and why not a
  scheduler plugin (consulted before affinity; native code tied to the build).

### Task 7: Account strategies and priorities, compiled into the engine

**Files:**
- Create: `internal/store/migrations/0008_account_routing.sql`, `internal/accounts/strategy.go`,
  `internal/accounts/strategy_test.go`
- Modify: `internal/store/accounts.go`, `internal/engine/cliproxyapi/types.go` (`Credential.Priority`),
  `internal/engine/cliproxyapi/client.go`, `internal/accounts/reconcile.go` (`settingsPatch`, `reconcileSettings`,
  `reconcileCredentials`), `internal/supervise/cliproxyapi.go` (`cliproxyRouting`, `CLIProxyAPIEngine`),
  `cmd/remora/serve.go`
- Test: `internal/store/{accounts,store}_test.go` (count 7), `internal/engine/cliproxyapi/client_test.go`,
  `internal/accounts/reconcile_test.go`, `internal/supervise/{cliproxyapi,engines}_test.go`

**Interfaces:**
- Consumes: Task 1's `alias.Strategy`; Task 5's engine keys.
- Produces:

```sql
-- 0008_account_routing.sql
ALTER TABLE accounts ADD COLUMN priority INTEGER NOT NULL DEFAULT 0;
-- One strategy per subscription provider (user-facing name); a provider without a row uses round-robin.
CREATE TABLE account_strategies (provider TEXT PRIMARY KEY, strategy TEXT NOT NULL) STRICT;
```

```go
// internal/store: Account gains Priority int
func (s *Store) SetAccountPriority(ctx context.Context, id int64, priority int) error             // ErrNotFound
func (s *Store) AccountStrategies(ctx context.Context) (map[string]alias.Strategy, error)         // set rows only
func (s *Store) SetAccountStrategy(ctx context.Context, provider string, st alias.Strategy) error // upsert

// internal/engine/cliproxyapi: Credential gains Priority int `json:"priority"`
func (c *Client) SetCredentialPriority(ctx context.Context, name string, priority int) error // PATCH /credentials/fields

// internal/accounts. NativePriorities: credential → engine priority over one provider's rows. priority → the row's
// number; fill-first → distinct n-1..0 by Priority desc, CreatedAt asc, ID asc; the other four → 0 (one tier).
func NativePriorities(st alias.Strategy, rows []store.Account) map[string]int
// EngineStrategies: every cliproxyapi.Providers() entry keyed by Engine name; random, least-used, p2c as written,
// everything else "round-robin".
func EngineStrategies(stored map[string]alias.Strategy) map[string]string
func (s *Service) Strategies(ctx context.Context) (map[string]alias.Strategy, error)        // all subscription providers, default round-robin
func (s *Service) SetStrategy(ctx context.Context, provider string, st alias.Strategy) error // ErrUnknownProvider
func (s *Service) SetPriority(ctx context.Context, id int64, priority int) error             // ErrNotFound

func settingsPatch(key string, twice bool, providerStrategies map[string]string) map[string]any
func CLIProxyAPIEngine(cfg config.Config, c Credentials, providerStrategies map[string]string) (Engine, error)
// cliproxyRouting gains ProviderStrategies map[string]string `yaml:"provider-strategies"`
```

`reconcileSettings` patches `EngineStrategies(store.AccountStrategies())`. `reconcileCredentials`, after the enabled
state, calls `SetCredentialPriority` only where `NativePriorities` differs from the engine's (missing credentials
skipped). `SetStrategy`/`SetPriority` write the store, then run `s.Reconcile(ctx)`. `serve.go` passes
`accounts.EngineStrategies(st.AccountStrategies(ctx))` to `CLIProxyAPIEngine`. Engine provider ids stay inside these
functions.

- [ ] **Step 1: Write the failing tests.**

```go
func TestNativePriorities(t *testing.T) // rows a(p5,t1,id1) b(p0,t0,id2) c(p0,t0,id3): round-robin, random, least-used,
// p2c → all 0; priority → {a:5 b:0 c:0}; fill-first → {a:2 b:1 c:0} (b before c: lower id)
func TestEngineStrategies(t *testing.T) // {} → every engine provider "round-robin"; {codex:p2c, grok:fill-first} → codex "p2c", xai "round-robin"
func TestAccountPriorityAndStrategies(t *testing.T) // priority 7 read back; unknown id → ErrNotFound; strategy upsert, last wins
func TestSetCredentialPriority(t *testing.T)   // client: PATCH /credentials/fields body {"name":"x.json","priority":3}
// shared settings table: new row routing.provider-strategies == EngineStrategies(nil) in
// TestCLIProxyAPIEngineRendersSettings, TestReconcilePatchesSettings and TestSettingsPatchMatchesRenderedFile
func TestReconcileAppliesPriorities(t *testing.T) // codex fill-first, two rows at 0/0 → one PATCH (older → 1); next pass none
func TestSetStrategyAppliesNow(t *testing.T)  // SetStrategy(codex, p2c) → store row; engine PatchConfig routing.provider-strategies.codex "p2c"
func TestSetStrategyUnknownProvider(t *testing.T) // "nope" → ErrUnknownProvider, nothing stored
```

- [ ] **Step 2: Run** `go test -race ./internal/store/ ./internal/engine/cliproxyapi/ ./internal/accounts/ ./internal/supervise/`.
  Expected: FAIL (undefined names, count 6 != 7).
- [ ] **Step 3: Implement** as specified.
- [ ] **Step 4: Run** the same command and `go build ./cmd/...`. Expected: PASS.
- [ ] **Step 5: Commit** `feat(accounts): per-provider strategy and per-account priority, compiled into the engine`
  (body: the mapping and why the spreading three use one tier).

### Task 8: Account strategies in the API and `remora.yaml`

**Files:**
- Modify: `api/openapi.yaml` (regenerate Go and web clients), `internal/api/{accounts,ports,adapters,server}.go`,
  `internal/config/{file,ports,plan,apply,export}.go`, both `remora.schema.json` copies
- Test: `internal/api/{accounts,fakes}_test.go`, `internal/config/{file,service,fakes}_test.go`

**Interfaces:**
- Consumes: Task 7's `Service.Strategies`, `SetStrategy`, `SetPriority`.
- Produces (OpenAPI): `Account.priority` (required integer); `AccountUpdate` with optional `enabled` and `priority`,
  `minProperties: 1`; `GET /api/accounts/strategies` (`listAccountStrategies`) → `AccountStrategyList {items:
  [AccountStrategy {provider, strategy: RoutingStrategy}]}`, every subscription provider sorted by name;
  `PUT /api/accounts/strategies/{provider}` (`setAccountStrategy`) body `{strategy}` → 200 `AccountStrategy`; unknown
  provider → 400 (existing `ErrUnknownProvider` mapping), bad strategy → 400 with Task 1's copy.
- Produces (Go):

```go
// internal/api ports: Accounts gains
SetPriority(ctx context.Context, id string, priority int) error
Strategies(ctx context.Context) (map[string]alias.Strategy, error)
SetStrategy(ctx context.Context, provider string, st alias.Strategy) error
// Account gains Priority int

// internal/config
// File gains AccountStrategies map[string]string `yaml:"account_strategies,omitempty"`
type AccountStrategies interface {
    Strategies(ctx context.Context) (map[string]alias.Strategy, error)
    SetStrategy(ctx context.Context, provider string, st alias.Strategy) error
}
func NewService(p Providers, a Aliases, k Keys, as AccountStrategies, batch Batch) *Service
```

Validation: a key that is not `cliproxyapi.IsSubscription` → `account_strategies.<key>: "<key>" is not a subscription
provider`; a bad value → Task 1's file copy. Plan: a differing provider → `Change{Op: "update", Kind:
"account_strategy", Name: provider, Fields: ["strategy"]}` after the key steps; with prune, an omitted provider not on
`round-robin` is reset to it (nothing is deleted). Apply runs these steps **after** the batch commits: they write the
OAuth engine and a store table, and a second store writer inside the batch's write transaction would block; a failure
is an `*ApplyError`, the batch's changes stay. Export lists only non-`round-robin` providers (`nil` when none). Schema:
values are the six-name enum; keys are checked in Go.

- [ ] **Step 1: Write the failing tests.**

```go
func TestListAccountStrategies(t *testing.T) // every subscription provider once, default "round-robin", sorted
func TestSetAccountStrategy(t *testing.T)    // codex p2c → 200; "bogus" → 400 copy; provider "nope" → 400
func TestUpdateAccountPartial(t *testing.T) // {"priority":4} → SetPriority only; {"enabled":false} → SetEnabled only; {} → 400
func TestValidateAccountStrategies(t *testing.T) // grok: p2c ok; xai: p2c → not a subscription provider; codex: bogus → error
func TestPlanAccountStrategies(t *testing.T)     // differing → one update; equal → none; prune, stored {grok: p2c}, file {} → reset
func TestApplyAccountStrategiesAfterBatch(t *testing.T) // fake Batch records order: SetStrategy runs after the batch returned
func TestExportAccountStrategies(t *testing.T)   // stored {codex: p2c, grok: round-robin} → {codex: p2c}; none → nil
```

- [ ] **Step 2: Run** `go test -race ./internal/api/ ./internal/config/`. Expected: FAIL.
- [ ] **Step 3: Implement**, then `go generate ./internal/api/gen` and `cd web && bun run gen`.
- [ ] **Step 4: Run** `go test -race ./... && golangci-lint run && cd web && bun run typecheck && bun run test`.
  Expected: PASS.
- [ ] **Step 5: e2e round trip.** Add to `test/e2e/strategy_test.go`: `TestAccountStrategyRoundTrip` —
  `PUT /api/accounts/strategies/codex {"strategy":"least-used"}` → 200; `GET /api/accounts/strategies` lists codex
  `least-used`, antigravity `round-robin`; `GET /api/config/export` has `account_strategies: {codex: least-used}`;
  `t.Cleanup` puts codex back to `round-robin`. Run
  `make image && go test -tags e2e -count=1 ./test/e2e/ -run AccountStrategy`. Expected: PASS.
- [ ] **Step 6: Commit** `feat(api): account strategies and priorities in the API and remora.yaml`.

### Task 9: Console controls

**Files:**
- Modify (under `web/src/routes/_app/`): `aliases/-lib/alias-form.ts`, `aliases/$name.tsx`,
  `providers/-lib/accounts.ts`, `providers/$providerId.tsx`, `providers/-components/AccountsTable.tsx`
- Test: `aliases/-lib/alias-form.test.ts`, `aliases/-route.test.tsx`, `providers/-components/AccountsTable.test.tsx`,
  `providers/-route.test.tsx`

**Interfaces:**
- Consumes: the regenerated client (`listAccountStrategiesOptions`, `setAccountStrategyMutation`,
  `updateAccountMutation`, `RoutingStrategy`).
- Produces: `ALIAS_STRATEGIES` in `alias-form.ts` and `ACCOUNT_STRATEGIES` in `accounts.ts`, each
  `{ id: RoutingStrategy; label: string; description: string }[]` in the order of Task 1's list. Copy (plain words, no
  engine name):

| id | alias description | account description |
|---|---|---|
| priority | Try members in order. | Highest priority first; equal priorities take turns. |
| fill-first | Same as priority for an alias. | One account at a time: highest priority, then oldest. |
| round-robin | Members take turns. | Accounts take turns. |
| random | A random member goes first. | A random account. |
| p2c | Two random members; the less busy one goes first. | Two random accounts; the less busy one serves. |
| least-used | The least busy member goes first. | The least busy account. |

  "Busy" means requests started in the last minute; the Select's description line says so once.

- [ ] **Step 1: Write the failing tests.** `alias-form.test.ts`: a new draft is `priority`, `toAlias` always sends
  `strategy`, a loaded `p2c` survives a save. Aliases route: choosing `least-used` saves `strategy: "least-used"`.
  `AccountsTable.test.tsx`: Priority column shows each number; entering 5 calls `onPriority(account, 5)`; the field's
  `aria-label` names the account. Providers route: choosing `fill-first` sends `PUT /api/accounts/strategies/<provider>`
  `{"strategy":"fill-first"}`; under any other strategy than `priority`/`fill-first` a line says priorities do not apply.
- [ ] **Step 2: Run** `cd web && bun run test`. Expected: FAIL.
- [ ] **Step 3: Implement.** Alias form: a Strategy `Select` beside Kind, built like it, descriptions as second lines.
  Provider page: a Strategy `Select` in the `SpecSheet` of a subscription provider; `AccountsTable`: an editable
  Priority column (HeroUI `NumberField`, step 1) on `updateAccountMutation({ body: { priority } })`. Both reuse the
  page's mutation and toast pattern and invalidate the accounts and strategies queries.
- [ ] **Step 4: Run** `cd web && bun run typecheck && bun run lint && bun run format:check && bun run test && bun run build`.
  Expected: every command exits 0.
- [ ] **Step 5: Commit** `feat(web): strategy controls for aliases and account pools, editable account priority`.

### Task 10: Final gates and the PR

- [ ] **Step 1:** `make lint test`. Expected: PASS.
- [ ] **Step 2:** `make image fakes-image e2e`. Expected: PASS, the whole suite.
- [ ] **Step 3:** `make bench && make bench`. Gate: each branch `chat c=10` p99 ≤ `max(b1, b2) + |b1 − b2|`. If it
  fails, profile (`REMORA_PPROF_ADDR`) before changing code; the likely cost is front-door rule evaluation.
- [ ] **Step 4:** One PR `routing-strategies` → `main`: changes per level, the migration footer, the fork row
  (`pending`), the four bench lines plus `chat spread c=10`.

---

## Self-review

- **Spec coverage.** Names, migration, CHANGELOG: 1; rules, publish, `Spread` gone: 2; pick, counter, WebSocket: 3–4;
  engine patch and row: 5–6; account store, compile, API, `account_strategies`: 7–8; UI: 9; e2e: 4, 8; bench: 0, 10.
- **Calls the spec leaves open.** Default rule ordered by exclusion (engine facts). At account level `random`,
  `least-used`, `p2c` use one tier like `round-robin`; only `priority` and `fill-first` read priority numbers. Account
  strategies apply after the front-door batch. Every forwarded response counts as a start, since members are shared.
- **Open risks.** Plan 11's parallel migration and Accounts-page edits. Header non-forwarding is proven only by e2e.
  Account-level spreading is proven by the fork's unit tests and the engine round trip; no fake stands in for the
  OAuth engine's pool end to end.
