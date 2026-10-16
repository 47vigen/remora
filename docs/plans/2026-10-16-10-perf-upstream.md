# Performance: Upstream Latency and Quality Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Cut time to first token on the subscription pool without any silent quality loss: keep each conversation on
one account so the provider's prompt cache hits, let an alias member carry an explicit thinking level, stop the front
door from answering a rejected request with another model, stop it repeating reasoning a third time, and say so when
accounts drop out of the pool.

**Architecture:** remora derives one affinity key per conversation in `internal/proxy` and sends it to the front door
as `X-Bf-Eh-X-Session-Affinity`; the front door forwards it as `X-Session-Affinity`, which the OAuth engine binds to
one account once remora turns on `routing.session-affinity`. A thinking suffix is part of a member's model string,
validated by the alias API and passed untouched to the OAuth engine, which parses it. Two front-door changes ride on
the existing fork branch (`docs/patches.md`). Pool health is computed in `internal/accounts` from the engine's
credential list and shown on `/healthz`, `/api/health` and the console overview.

**Tech Stack:** Go 1.27, `net/http`, `crypto/sha256`, `go.yaml.in/yaml/v3`, oapi-codegen, Bun + Vite + HeroUI v3
(`web/`), Docker Compose e2e with the fakes image.

**Spec:** `docs/specs/2026-10-16-performance-design.md`, PR 3 ("Upstream latency and quality"), minus the load
harness, which plan 08 lands. Evidence: the spec's Evidence section.

Engine versions verified: Bifrost fork commit `179c29b` on `transports/v2.3.0`, CLIProxyAPI `v8.0.30`
(`engines/versions.env`). `B/` = `engines/bifrost`, `C/` = `engines/cliproxyapi`.

## Global Constraints

- Never edit `engines/` in this repo's history except by moving the submodule pin. Front-door changes are commits on
  the fork branch, each with a `docs/patches.md` row (`docs/patches.md`, plan 04 Task 8 is the procedure).
- Every OAuth engine key goes in **both** `cliproxyConfig` in `internal/supervise/cliproxyapi.go` (boot) and
  `settingsPatch` in `internal/accounts/reconcile.go` (runtime), and into plan 08's shared settings table in their
  tests, so the two cannot drift.
- Engine names, keys and ids never reach a client or a public endpoint. `/healthz` is unauthenticated: it carries a
  count, never provider names or account labels.
- No silent quality change: no default thinking level, no new fallback, no change to the OAuth engine's HTTP
  fingerprint toward the provider.
- Tests before code. `go test -race ./...` and `golangci-lint run` stay green after every task.
- Conventional Commits with scopes; each body says what changed and why, and cites the engine lines it relies on.
- Branch `perf-upstream`, one PR to `main`. Plans 08 and 09 are merged first.

## Review Focus

1. **Two conversations that share a long system prompt.** They must get different affinity keys, and every turn of
   one conversation must keep its key. Test: Task 1, `TestAffinityKeyIgnoresSystemPrompt`.
2. **A thinking suffix on an API-key member** (`up-a/m1(low)`) would reach a third-party provider as an unknown
   model. The alias API must refuse it, and accept `antigravity/gemini-3-flash(low)`. Test: Task 4,
   `TestFromGenAliasThinkingSuffix`.
3. **A primary that answers 400 because of a region, a model or a rate limit.** It must still fall back; only a
   refusal of the request itself stops the chain. Test: Task 6, `TestScenarioFallbacksStopOnCallerFault`.
4. **A reasoning entry that carries a signature** (Anthropic thinking replayed on tool turns). It must survive; only
   the synthesised text-only copy is dropped. Test: Task 6, `TestSynthesizedReasoningDetailsNotEmitted`.
5. **An account the owner disabled, or one cooling on a 429.** Neither counts as out of the pool; a refresh failure
   and a missing credential do, and `/healthz` stays 200. Test: Task 8, `TestPoolCountsOnlyDroppedAccounts`.

## Names consumed from plans 08 and 09

| from | name |
|---|---|
| 08 | The extended `cliproxyConfig` (`Routing.Retry`, `Cooldown.TransientErrorCooldownSeconds`, `Requests`, `OAuth.Providers`) and `settingsPatch`; the shared settings table in `internal/supervise/cliproxyapi_test.go` and `internal/accounts/reconcile_test.go` (`TestCLIProxyAPIEngineRendersSettings`, `TestReconcilePatchesSettings`, `TestReconcileAddsNewSettings`). `make bench` and `bench-results/baseline-0.2.0.txt`. |
| 09 | The one-pass body scanner in `internal/proxy`, whose result rides on `reqInfo`. This plan needs one accessor from it: the raw JSON of the first `messages[]`/`input[]` element whose `role` is `"user"` (or a string `input`), nil when there is none. Plan 09 names it `reqInfo.spans bodySpans` with `firstUser span` (`info.body[sp.start:sp.end]`, absent when `end == 0`); below, `info.scan.FirstUser(info.body)` is shorthand for that slice. Plan 09's `firstUser` covers array elements only, so a string `input` is handled here: when `firstUser` is absent and `spans.input` is a JSON string, hash that value. This plan adds one top-level key to the same scan: `bodySpans.promptCacheKey span`, set in `scanBody` for the key spelled exactly `prompt_cache_key` with a string value; `info.scan.PromptCacheKey(info.body)` below is shorthand for that value unquoted, "" when absent. Do not add a second parser. |

## Engine facts this plan relies on

- The OAuth engine reads `X-Session-ID`, then `X-Session-Affinity`, as an explicit session
  (`C/sdk/cliproxy/session/info.go:475-500`); nothing in it reads `X-Opencode-Session`. Explicit ids are absolute,
  the LCP matcher runs only without one (`C/sdk/cliproxy/auth/selector.go:1100-1130,1809-1827`).
- Keys `routing.session-affinity`, `session-affinity-ttl`, `session-affinity-rebind-on`
  (`C/internal/config/config_types.go:394-420`). `quota` keeps the binding across 408/5xx and rebinds on
  429/401/402/403/404 (`:412-419`). TTL defaults to 1h; `quota` applies only with affinity on
  (`C/sdk/cliproxy/service_config.go:65-97`). A config reload swaps the selector when the routing state changes, so a
  runtime PATCH takes effect without a restart (`service_config.go:259-262`).
- The front door turns `x-bf-eh-<name>` into a request header `<name>` (`B/transports/bifrost-http/lib/ctx.go:635-652`)
  unless a header filter is configured, and remora configures none. Providers apply them in
  `B/core/providers/utils/utils.go:1606-1619`; the OpenAI-type provider (`remora-oauth` and every
  `openai-compatible` provider) calls it for chat, chat stream and responses (`B/core/providers/openai/openai.go:180,319,425`).
- Thinking suffix: `ParseSuffix` takes the last `(` of a model ending in `)` (`C/internal/thinking/suffix.go:23-43`).
  Accepted values: `none`, `auto`, `-1`, `minimal|low|medium|high|xhigh|max`, a non-negative integer (0 = off)
  (`C/internal/thinking/apply.go:461-492`, `suffix.go:127-146`). Routing strips it
  (`C/sdk/api/handlers/handlers_routing.go:165-190`), executors strip it (`antigravity_executor_stream.go:48`). A
  suffix wins over the body's `reasoning_effort` (`apply.go:313-321`). A model without thinking ignores it
  (`apply.go:290-308`).
- Fallbacks: `shouldTryFallbacks` stops on a caller fault only when the chain was inferred
  (`B/core/bifrost.go:5583-5618`, the test is `B/core/bifrost_test.go:7666-7700`). A caller fault is
  400/409/413/415/422 with a provider error body, classified only after rate-limit, region, credential and model
  signals (`B/core/failureclass.go:224-327`).
- `reasoning_details` is synthesised on parse (`B/core/schemas/chatcompletions.go:1632-1683` and `:1883-1929`) and
  read internally by the Responses and Anthropic converters (`B/core/schemas/mux.go`,
  `B/core/providers/anthropic/chat.go`), so it is dropped on marshal (`:1615-1627`, `:1866-1878`), never on parse.
- A refresh failure leaves a credential `Unavailable` with status `error` and, when terminal, no retry time
  (`C/sdk/cliproxy/auth/conductor_refresh.go:750-835`). A 429 also sets status `error`, but with a cooldown whose
  reason is `quota` (`conductor_cooldown.go:2403-2424`, `cooldown_view.go:85-150`).
- The engine keeps no per-credential in-flight count outside its Home mode
  (`C/internal/config/credential_concurrency.go:22`). **The spec's per-account in-flight stat is dropped.** Per-account
  `success`/`failed` and cooldowns with reason `quota` are already in the accounts view (`internal/accounts/manage.go:30-60`),
  which is the `RATE_LIMIT_EXCEEDED` signal the spec wants.
- `scripts/check-engine-pins.sh` reads the **first** `docs/patches.md` row per engine. Every `bifrost` row therefore
  carries the fork branch's tip commit.

---

## File map

```
internal/proxy/session.go                 affinity key (Task 1)
internal/proxy/proxy.go:298               send X-Bf-Eh-X-Session-Affinity (Task 1)
internal/supervise/cliproxyapi.go         affinity keys at boot (Task 2)
internal/accounts/reconcile.go            affinity keys in settingsPatch (Task 2)
test/e2e/affinity_test.go                 header reaches the upstream (Task 3)
internal/alias/thinking.go                suffix parsing (Task 4)
internal/api/aliases.go                   suffix validation (Task 4)
api/openapi.yaml                          member and health descriptions (Tasks 4, 8)
web/src/routes/_app/aliases/-lib/alias-form.ts, -components/MemberList.tsx   suffix display (Task 5)
README.md                                 thinking level section (Task 5)
B/ fork branch remora/perf-upstream       two patches (Task 6)
.gitmodules unchanged; engines/bifrost pin, docs/patches.md   (Task 7)
test/e2e/fallback_test.go, test/e2e/reasoning_test.go          (Task 7)
internal/accounts/pool.go                 pool health (Task 8)
internal/api/system.go, internal/api/ports.go, internal/api/adapters.go, cmd/remora/serve.go   (Task 8)
web/src/routes/_app/(overview)/-components/PoolAlert.tsx       (Task 8)
```

---

### Task 1: The affinity key

**Files:**
- Modify: `internal/proxy/session.go`, `internal/proxy/proxy.go:298`
- Test: `internal/proxy/session_test.go`

**Interfaces:**
- Consumes: plan 09's `info.scan.FirstUser`, and `info.scan.PromptCacheKey` (added here to plan 09's scan, one case).
- Produces:

```go
const headerForwardAffinity = "X-Bf-Eh-X-Session-Affinity"

// affinityKey is the conversation's account-affinity key: the client's own id from X-Session-ID, X-Opencode-Session
// or the body's prompt_cache_key, in that order; otherwise "aff_" + 32 hex chars of
// sha256("remora-affinity\x00" + vk + "\x00" + firstUser). "" when there is no client id and no user message.
func affinityKey(h http.Header, vk, promptCacheKey string, firstUser []byte) string
```

The hash takes the client key (`vk`), so two consumers who open with the same "hi" do not share an account. A message
before the first user message that is neither system nor developer (rare) is not in the hash: plan 09 gives the span
of the first user message only. `shortcut: one span, not the whole prefix; widen if plan 09 exposes message spans`.

- [ ] **Step 1: Write the failing tests** in `session_test.go`:

```go
func TestAffinityKeyPrecedence(t *testing.T) {
    // X-Session-ID "s1" + X-Opencode-Session "o1" + pck "p1" → "s1"
    // only X-Opencode-Session "o1" + pck "p1" → "o1"; only pck "p1" → "p1"
    // none, firstUser `{"role":"user","content":"x"}` → matches ^aff_[0-9a-f]{32}$
    // none, firstUser nil → ""
}
func TestAffinityKeyIgnoresSystemPrompt(t *testing.T) {
    // bodies built with the real scanner: same system, different first user → keys differ
    // different system, same first user → keys equal
    // turn 1 vs turn 3 of one conversation (same system + first user, more messages after) → keys equal
    // Responses body {"instructions":"…","input":"hello"} vs {"input":"hello"} → keys equal
    // same body, vk "a" vs vk "b" → keys differ
}
func TestAffinityKeyRejectsUnusableClientID(t *testing.T) {
    // X-Session-ID of 257 bytes, or containing "\n" → ignored, next source wins (same rule as opencodeSession)
}
func TestRewriteSetsAffinity(t *testing.T) {
    // rewrite with X-Session-ID "abc": out header X-Bf-Eh-X-Session-Affinity == "abc"
    // incoming forged "X-Bf-Eh-X-Session-Affinity: evil" and no usable source → header absent
    // embeddings body (no messages) → header absent; X-Bf-Eh-X-Opencode-Session still set as before
}
```

- [ ] **Step 2: Run** `go test -race ./internal/proxy/ -run 'Affinity'`. Expected: FAIL (`undefined: affinityKey`).
- [ ] **Step 3: Implement.** `affinityKey` as above, reusing `maxSessionLen` and `isControl`. In `rewrite`, after the
  OpenCode header: `if k := affinityKey(pr.In.Header, info.vk, info.scan.PromptCacheKey(info.body), info.scan.FirstUser(info.body)); k != "" { pr.Out.Header.Set(headerForwardAffinity, k) }`.
  `setUpstreamHeaders` already deletes every incoming `x-bf-*`.
- [ ] **Step 4: Run** the same command. Expected: PASS.
- [ ] **Step 5: Commit** `feat(proxy): send a per-conversation account-affinity key`. Body: why the OpenCode id
  (first message, usually a shared system prompt) would pin everyone to one account; the engine header order
  (`C/sdk/cliproxy/session/info.go:475-500`).

### Task 2: Turn on affinity in the OAuth engine

**Files:**
- Modify: `internal/supervise/cliproxyapi.go` (`cliproxyRouting`), `internal/accounts/reconcile.go` (`settingsPatch`)
- Test: `internal/supervise/cliproxyapi_test.go`, `internal/accounts/reconcile_test.go`

**Interfaces:**
- Produces: `cliproxyRouting` gains `SessionAffinity bool \`yaml:"session-affinity"\``,
  `SessionAffinityTTL string \`yaml:"session-affinity-ttl"\``, `SessionAffinityRebindOn string \`yaml:"session-affinity-rebind-on"\``.
  `settingsPatch` gains the same three under `routing`.

- [ ] **Step 1: Write the failing tests.** Add three rows to plan 08's shared table:
  `routing.session-affinity == true`, `routing.session-affinity-ttl == "1h"`,
  `routing.session-affinity-rebind-on == "quota"`. `TestCLIProxyAPIEngineRendersSettings` and
  `TestReconcilePatchesSettings` then assert them in the file and in the patch.
  `TestReconcileAddsNewSettings`: a fake engine holding plan 08's keys but not these gets one PATCH that adds them.
- [ ] **Step 2: Run** `go test -race ./internal/supervise/ ./internal/accounts/`. Expected: FAIL on the three rows.
- [ ] **Step 3: Implement** the fields and values (`true`, `"1h"`, `"quota"`).
- [ ] **Step 4: Run** the same command. Expected: PASS. If `engine`-tagged tests cover config loading, also
  `go test -tags engine ./internal/engine/cliproxyapi/`. Expected: PASS.
- [ ] **Step 5: Commit** `perf(accounts): keep a conversation on one account for prompt-cache hits`. Body: the keys
  and lines (`config_types.go:394-420`, `service_config.go:65-97,259-262`); rebind on quota costs one turn's cache,
  not quality.

### Task 3: e2e — the key reaches the upstream

**Files:**
- Create: `test/e2e/affinity_test.go` (`//go:build e2e`)

**Interfaces:**
- Consumes: `chat`, `received`, `resetFakes`, `persian` (`test/e2e`), `control.RecordedRequest.Header`
  (`test/fakes/control/control.go:36-43`). The `fast` alias reaches `up-a`, an `openai-compatible` provider: the same
  front-door code path as `remora-oauth`. No fakes change: the fake already records request headers.

- [ ] **Step 1: Write the tests.**

```go
func TestAffinityHeaderForwarded(t *testing.T) {
    // POST fast with header X-Session-ID "e2e-1": up-a's request has Header.Get("X-Session-Affinity") == "e2e-1"
    // and no header whose name starts with "X-Bf-"
}
func TestAffinityDerivedPerConversation(t *testing.T) {
    // A: [system S, user persian]; B: [system S, user persian+"2"]; A2: A + assistant + user "next"
    // affinity(A) != affinity(B); affinity(A) == affinity(A2); each matches ^aff_[0-9a-f]{32}$
}
```

- [ ] **Step 2: Run** `make image && go test -tags e2e -count=1 ./test/e2e/ -run Affinity`. Expected: PASS
  (Tasks 1–2 are in). To prove the test bites, comment out the `Set` in `rewrite`, rebuild, rerun: FAIL; restore.
- [ ] **Step 3: Commit** `test(e2e): the affinity key reaches the upstream through the front door`.

### Task 4: Thinking suffix on alias members

**Files:**
- Create: `internal/alias/thinking.go`, `internal/alias/thinking_test.go`
- Modify: `internal/api/aliases.go` (`fromGenAlias`), `api/openapi.yaml` (`Alias.members` description), regenerate
  `internal/api/gen` (`go generate ./internal/api/gen`)
- Test: `internal/api/aliases_test.go`, `internal/alias/compile_test.go`, `internal/reconcile/oauth_test.go`

**Interfaces:**
- Produces:

```go
// ParseThinking splits a member ref "<provider>/<model>(<level>)" into the ref without the suffix and the level.
// level is "" when the ref has no suffix. ok is false when a suffix is present but is not one of minimal, low,
// medium, high, or a whole number of thinking tokens (0 = off).
func ParseThinking(ref string) (base, level string, ok bool)
```

Validation in `fromGenAlias`: for each member that contains `/`, `ParseThinking`; `!ok` → 400 `thinking level must be
minimal, low, medium, high or a token budget; got "<level>"`; a level on a provider that is not a subscription
provider (`cliproxyapi.ProviderByName(provider)` false) → 400 `a thinking level applies only to subscription models`.
The engine also accepts `xhigh`, `max`, `none`, `auto`; they stay out until someone asks. Compile and
`reconcile.ToEngineTarget` need no change: the suffix stays in `Target.Model`.

- [ ] **Step 1: Write the failing tests.**

```go
func TestParseThinking(t *testing.T) // table:
// "antigravity/gemini-3-flash(low)" → base "antigravity/gemini-3-flash", "low", true
// "…(minimal)", "…(medium)", "…(high)", "…(1024)", "…(0)" → ok
// "antigravity/gemini-3-flash" → base unchanged, "", true
// "…(extreme)", "…(-1)", "…()", "…(1.5)" → ok false
// "antigravity/foo(bar)baz" → no suffix, ok true (ParseSuffix needs a closing ")" at the end)
func TestFromGenAliasThinkingSuffix(t *testing.T)
// members ["antigravity/gemini-3-flash(low)"] → accepted, stored ref unchanged
// ["up-a/m1(low)"] → 400 "applies only to subscription models"
// ["antigravity/gemini-3-flash(extreme)"] → 400 naming "extreme"
func TestCompileKeepsThinkingSuffix(t *testing.T)
// members [antigravity/x(low), antigravity/x] → two targets, Model "x(low)" then "x"
func TestToEngineTargetKeepsSuffix(t *testing.T)
// {antigravity, gemini-3-flash(low)} → {remora-oauth, antigravity/gemini-3-flash(low)}
```

- [ ] **Step 2: Run** `go test -race ./internal/alias/ ./internal/api/ ./internal/reconcile/`. Expected: FAIL
  (`undefined: alias.ParseThinking`; the API test accepts `up-a/m1(low)`).
- [ ] **Step 3: Implement** `ParseThinking` (`strings.LastIndexByte`, `strconv.Atoi`) and the two checks. In
  `api/openapi.yaml`, extend the `members` description: a subscription model may end in `(minimal)`, `(low)`,
  `(medium)`, `(high)` or a token budget; it overrides the client's `reasoning_effort` for that member. Regenerate.
- [ ] **Step 4: Run** the same command. Expected: PASS.
- [ ] **Step 5: e2e, front door keeps the parentheses.** Add to `test/e2e/chat_test.go`:
  `TestModelSuffixReachesUpstream`: a direct request for `up-a/m1(low)` with the `full` key → up-a's body `model`
  is `"m1(low)"` byte for byte. This proves the front door does not rewrite the suffix on the OpenAI-type provider
  that `remora-oauth` also uses (no fake can stand in for the OAuth engine itself). Run
  `make image && go test -tags e2e -count=1 ./test/e2e/ -run TestModelSuffixReachesUpstream`. Expected: PASS.
- [ ] **Step 6: Commit** `feat(alias): accept a thinking level on subscription members`.

### Task 5: Console display and docs

**Files:**
- Modify: `web/src/routes/_app/aliases/-lib/alias-form.ts`, `web/src/routes/_app/aliases/-components/MemberList.tsx`,
  `README.md`
- Test: `web/src/routes/_app/aliases/-components/MemberList.test.tsx`

**Interfaces:**
- Produces: `export function thinkingOf(ref: string): { base: string; level?: string }` in `alias-form.ts` (the
  display twin of `ParseThinking`; the server stays the validator).

- [ ] **Step 1: Write the failing test.** `MemberList` with `["antigravity/gemini-3-flash(low)", "up-a/m1"]`: the
  first row shows `antigravity/gemini-3-flash` and a chip reading `thinking low`; a `(1024)` member shows
  `thinking 1024 tokens`; the second row has no chip. Remove and reorder still use the full ref.
- [ ] **Step 2: Run** `cd web && bun test MemberList`. Expected: FAIL (no chip).
- [ ] **Step 3: Implement.** In the row, render `thinkingOf(ref).base` and, when `level` is set, a
  `<Chip size="sm" variant="soft">` beside the existing `cooling` chip. Keep `textValue={ref}` and every
  `aria-label` on the full ref. The add box already accepts typed values (`AddCombo` `allowsCustomValue`), so no
  picker change.
- [ ] **Step 4: Run** `cd web && bun test && bun run build`. Expected: PASS, build exits 0.
- [ ] **Step 5: Docs.** README, a `## Thinking level` section after "Dropped parameters": the suffix forms; only for
  subscription models; it overrides `reasoning_effort` for that member; a model without thinking ignores it; no
  existing alias changes; a fast tier is a named alias such as `flash-fast` with members `antigravity/<flash>(low)`,
  never a default. No engine name in the text.
- [ ] **Step 6: Commit** `feat(web): show a member's thinking level` and `docs: thinking level on alias members`.

### Task 6: Front-door patches on the fork

**Files (in the fork, `engines/bifrost` checked out on a new branch):**
- Modify: `core/bifrost.go:5583-5618` (`shouldTryFallbacks`), `core/bifrost_test.go:7666-7700`
- Modify: `core/schemas/chatcompletions.go:1615-1627` and `:1866-1878` (the two `MarshalJSON`)
- Test: `core/schemas/reasoningcontentalias_test.go`

**Interfaces:**
- Produces: in `core/schemas/chatcompletions.go`,
  `func emittedReasoningDetails(reasoning *string, details []ChatReasoningDetails) []ChatReasoningDetails` — nil when
  `details` is exactly one entry of type `reasoning.text` with no `ID`, `Signature`, `Data` or `Summary` and `Text`
  equal to `*reasoning`; otherwise `details` unchanged. Both `MarshalJSON` use it on the copy they marshal.
- Produces: `shouldTryFallbacks` returns false for `ClassifyFailure(primaryErr) == FailureClassCallerFault` on every
  chain, not only inferred ones (drop `fallbacks[0].Inferred &&` at `:5610`, update the doc comment). The classifier
  is reused on purpose: a 400 that names a region, a model, a key or a rate limit is classed before caller fault and
  still falls back. It also covers 409 and 415 with an error body, which are refusals of the request too.
  `shouldContinueWithFallbacks` is unchanged: once the primary failed for another reason, a later member's 400 does
  not stop members after it.

- [ ] **Step 1: Branch from the pin.**
  `git -C engines/bifrost switch -c remora/perf-upstream 179c29b` (the fork remote is `origin`,
  `https://github.com/47vigen/bifrost.git`).
- [ ] **Step 2: Write the failing tests.**

```go
// core/bifrost_test.go — rename TestScenarioInferredFallbacksStopOnCallerFault → TestScenarioFallbacksStopOnCallerFault;
// flip "caller-named chain, caller fault 400 still falls back" to wantFallback false, and add, for both chain kinds
// and both stream values:
//   caller-named 413 and 422 with callerFaultBody → no fallback, primary error returned, provB never called
//   caller-named 429, 503, 401, 404 model_not_found → fallback
//   caller-named 400 whose message names an unsupported region → fallback
// core/schemas/reasoningcontentalias_test.go
func TestSynthesizedReasoningDetailsNotEmitted(t *testing.T) {
    // unmarshal delta {"reasoning_content":"r"} → marshal: has "reasoning":"r" and "reasoning_content":"r", no "reasoning_details"
    // same for ChatAssistantMessage
    // delta with reasoning "r" and details [{type reasoning.text, text "r", signature "sig"}] → "reasoning_details" kept, signature "sig"
    // details [{type reasoning.encrypted, data "d"}] → kept
    // details [{type reasoning.text, text "other"}] (not equal to reasoning) → kept
}
```

- [ ] **Step 3: Run** `cd engines/bifrost/core && go test ./ ./schemas -run 'CallerFault|SynthesizedReasoning'`.
  Expected: FAIL (the flipped case falls back; `reasoning_details` present).
- [ ] **Step 4: Implement** the two changes. Keep the existing round-trip test
  (`reasoningcontentalias_test.go:121-134`) green: unmarshal synthesises again, which internal converters need.
- [ ] **Step 5: Run** `cd engines/bifrost/core && go test ./ ./schemas ./providers/openai/... ./providers/anthropic/...`.
  Expected: `ok` for each.
- [ ] **Step 6: Commit on the fork, two commits, and push:**
  `fix(core): do not fall back when the primary refuses the request itself` and
  `perf(schemas): do not emit the synthesised reasoning_details copy`, then
  `git -C engines/bifrost push origin remora/perf-upstream`.
- [ ] **Step 7: Upstream PRs** — ask the owner before this outward step. Base = Bifrost's default branch. The fallback
  one changes a documented upstream choice (named chains fall back on any error), so offer it behind a config flag if
  the maintainers prefer; record the URLs or `pending`.

### Task 7: Pin the fork, record the patches, e2e

**Files:**
- Modify: `engines/bifrost` (submodule pointer), `docs/patches.md`
- Modify: `test/e2e/fallback_test.go`; Create: `test/e2e/reasoning_test.go`

- [ ] **Step 1: Pin.** `git -C engines/bifrost checkout remora/perf-upstream`. `.gitmodules` already points at the fork.
- [ ] **Step 2: `docs/patches.md`.** Two new rows, engine `bifrost`, base tag `transports/v2.3.0`:
  `core/bifrost.go` / "no fallback when the primary refuses the request itself (400/409/413/415/422 with an error
  body)" / "a request the primary rejected is otherwise answered by another model"; `core/schemas/chatcompletions.go`
  / "do not emit the synthesised text-only `reasoning_details` entry" / "reasoning reached clients three times".
  Set **every** bifrost row's `fork commit`, the speaker row's included, to the branch tip: `check-engine-pins.sh`
  reads only the first row per engine.
- [ ] **Step 3: Run** `make check-pins`. Expected: `ok bifrost fork <tip> on transports/v2.3.0`.
- [ ] **Step 4: Write the e2e tests.**

```go
func TestNoFallbackOnCallerFault(t *testing.T) {
    // for status in 400, 413, 422, stream false/true:
    //   once up-a: {Status: status, Body: `{"error":{"message":"bad request","type":"invalid_request_error","param":"messages","code":"invalid_value"}}`}
    //   chat fast → status == status; X-Remora-Fallback absent or "false"; len(received(b)) == 0; len(received(a)) == 1
}
// TestFallbackWithinRequest (existing, 429/402) stays green: those still fall back.
func TestReasoningNotTripled(t *testing.T) {
    // non-stream: once up-a Body = chat completion with message {"role":"assistant","content":persian,"reasoning_content":"r"}
    //   remora body: choices[0].message has "reasoning" == "r", "reasoning_content" == "r", no key "reasoning_details"
    // stream: once up-a SSE = [delta {"reasoning_content":"r"}, delta {"content":persian}]
    //   no chunk contains "reasoning_details"; reasoning chunk carries "reasoning_content":"r"; content bytes == persian
}
```

- [ ] **Step 5: Run** `make image fakes-image && go test -tags e2e -count=1 ./test/e2e/ -run 'CallerFault|ReasoningNotTripled|FallbackWithinRequest'`.
  Expected: PASS. Prove the pin matters: check out `179c29b`, `make image`, rerun the two new tests: FAIL; restore.
- [ ] **Step 6: Commit** `build(engines): pin bifrost to the fork with the fallback and reasoning patches` and
  `test(e2e): no fallback on caller faults; reasoning emitted twice, not three times`.

### Task 8: Pool health

**Files:**
- Create: `internal/accounts/pool.go`, `internal/accounts/pool_test.go`,
  `web/src/routes/_app/(overview)/-components/PoolAlert.tsx`, `PoolAlert.test.tsx`
- Modify: `internal/api/system.go` (`HealthResponse`, `RegisterHealthz`, `GetHealth`), `internal/api/ports.go`
  (`System`), `internal/api/adapters.go` (`NewSystem`), `cmd/remora/serve.go:170`, `api/openapi.yaml` (`Health`),
  regenerated `internal/api/gen` and `web/src/client` (`bun run gen`), `web/src/routes/_app/(overview)/index.tsx`
- Test: `internal/api/system_test.go` (or the file that tests `RegisterHealthz`)

**Interfaces:**
- Produces:

```go
// internal/accounts/pool.go
type OutAccount struct{ ID int64; Label, Reason string } // Reason: "signed out", "missing", or the engine's scrubbed status message
type PoolStatus struct{ Provider string; Enabled int; Out []OutAccount }
// Pool reports, per subscription provider with at least one enabled account, which enabled accounts are out of the
// pool: the credential is missing, or it is unavailable without a cooldown, or its cooldown reason is
// invalid_grant, unauthorized or payment_required. Cooling on quota or a transient error is not out. One engine
// call (credentials), no model listing.
func (s *Service) Pool(ctx context.Context) ([]PoolStatus, error)

// internal/api/system.go
type PoolSource interface{ Pool(ctx context.Context) ([]accounts.PoolStatus, error) }
type HealthResponse struct {
    Status      string   `json:"status"`
    Down        []string `json:"down,omitempty"`
    AccountsOut int      `json:"accounts_out,omitempty"` // count only: /healthz is public
}
func RegisterHealthz(mux *http.ServeMux, src StatusSource, pool PoolSource)
// System gains: Pool(ctx) ([]accounts.PoolStatus, error); NewSystem(src StatusSource, pool PoolSource)
```

OpenAPI `Health` gains optional `pool: [{provider, enabled, out: [{id, label, reason}]}]`. A pool shrink never turns
`/healthz` to 503: an orchestrator would restart a node that is still serving. A `Pool` error leaves the detail out;
the engine being down already shows as `accounts` in `down`.

- [ ] **Step 1: Write the failing tests.**

```go
func TestPoolCountsOnlyDroppedAccounts(t *testing.T) {
    // cliproxyapitest fake + store rows, provider antigravity, 5 rows:
    //   ok (active); disabled row (Enabled false, engine Unavailable) → not counted, not in Enabled
    //   cooling: Unavailable, Status "error", Cooldowns [{Reason "quota", RetryAt now+30s}] → not out
    //   refresh failed: Unavailable, Status "error", no cooldowns, StatusMessage "unauthorized (refresh token invalid)" → out
    //   missing: row with no credential → out, Reason "missing"
    // Pool → [{Provider "antigravity", Enabled 4, Out: the two, by id}]
}
func TestHealthzReportsAccountsOut(t *testing.T) {
    // pool source with 2 out, engines up → 200, {"status":"ok","accounts_out":2}; body has no provider name or label
    // pool source erroring → 200, no accounts_out
}
func TestGetHealthPool(t *testing.T) // /api/health carries pool with provider, enabled, out labels
```

`PoolAlert.test.tsx`: given `pool: [{provider: "antigravity", enabled: 20, out: [a, b]}]` it renders one HeroUI
`Alert` reading `2 of 20 antigravity accounts are out of the pool`, listing both labels, linking to the accounts
page; with no `out` it renders nothing.

- [ ] **Step 2: Run** `go test -race ./internal/accounts/ ./internal/api/` and `cd web && bun test PoolAlert`.
  Expected: FAIL (`undefined: (*Service).Pool`; no `accounts_out`; component missing).
- [ ] **Step 3: Implement.** `Pool` reads `s.store.Accounts` and `s.credentials` (already in `manage.go:239`) and uses
  `scrub` on status messages. Wire `serve.go:170` as `api.RegisterHealthz(mux, sup, acct)` and `NewSystem(sup, acct)`.
  Edit `api/openapi.yaml`, run `go generate ./internal/api/gen` and `cd web && bun run gen`. Render `PoolAlert`
  at the top of the overview from `useQuery({...getHealthOptions(), refetchInterval: 30_000})`.
- [ ] **Step 4: Run** `go test -race ./...` and `cd web && bun test && bun run build`. Expected: PASS.
- [ ] **Step 5: No e2e.** The fakes cannot make the OAuth engine fail a refresh; the unit tests above cover it.
- [ ] **Step 6: Commit** `feat(accounts): report accounts that drop out of the pool` and
  `feat(web): alert when subscription accounts drop out of the pool`.

### Task 9: Gates and measurement

- [ ] **Step 1:** `make lint test`. Expected: 0 issues; every package `ok`.
- [ ] **Step 2:** `make image fakes-image e2e`. Expected: `ok github.com/47vigen/remora/test/e2e`.
- [ ] **Step 3:** `make bench`. Compare with plan 08's result file and `bench-results/baseline-0.2.0.txt`. Expected:
  no regression in overhead p50/p99 or requests per second (this PR adds one hash per request); `err=0` at every
  level. The bench upstream is a fake, so it cannot show the cache gain; quote the table in the PR.
- [ ] **Step 4:** The owner runs the live-pool check after merge, outside this repo: 200 concurrent and long
  multi-turn conversations against the real pool, reporting time to first token per account and the prompt-cache hit
  rate from `cachedContentTokenCount` in usage, against the same run before this PR. Nothing in the repo scripts it.
- [ ] **Step 5:** PR description: the engine facts above with their lines, the bench table, the dropped in-flight
  stat and why, and the upstream PR links or `pending`.

## Self-review notes

- Spec coverage: affinity key and header (Tasks 1, 3), engine keys (Task 2), thinking suffix in API, compile, console,
  docs (Tasks 4, 5), two front-door patches (Tasks 6, 7), pool health (Task 8), measurement (Task 9). Dropped:
  per-account in-flight stats (the engine keeps none outside Home mode).
- Deviations from the spec's letter: the fallback stop uses the front door's caller-fault class (adds 409/415, needs
  an error body, spares region/model/rate-limit 400s); a suffix is refused on API-key members; a suffix overrides the
  client's `reasoning_effort` for that member.
