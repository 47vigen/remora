# The OAuth subscription layer: CLIProxyAPI, cliproxy-rs, CLIProxyAPIPlus

Date: 2026-10-14. Sources: `router-for-me/CLIProxyAPI` v8.0.28, `vayungodara/cliproxy-rs` 0.2.5,
`jc01rho/CLIProxyAPIPlus` v8.0.28-1, read, not run, no real account used.
Verdict: upstream CLIProxyAPI owns the OAuth layer, as a separate process behind the front door.

## Why a separate layer at all

No front door (Bifrost, agentgateway, LiteLLM) logs into Google Antigravity or ChatGPT Codex with a subscription
account. That traffic is more than half of the reference deployment. CLIProxyAPI does exactly this and nothing
else well, so remora composes the two: the front door owns keys, aliases, fallbacks, embeddings, audio and
decisions; CLIProxyAPI owns subscription accounts and is registered in the front door as one OpenAI-compatible
provider.

## Capability matrix

| # | capability | CLIProxyAPI (Go) | cliproxy-rs (Rust) |
|---|---|---|---|
| 1 | Antigravity executor | ✅ first-class (`sdk/cliproxy/service_executors.go:309`, about 30 `antigravity_*` files) | ⚠️ ported; its own `docs/PARITY.md:56`: "No live Antigravity account has been used" |
| 1 | pooling (round-robin, weighted, fill-first, session affinity) | ✅ `config.example.yaml:149-178` | ✅ same format |
| 1 | 429 handling | ✅ classified: soft retry, same-account retry, short cooldown and switch, quota exhausted (`antigravity_executor_credits.go:26-47`); honours retryDelay with backoff (`sdk/cliproxy/auth/conductor_cooldown.go:909`) | ⚠️ cooldowns only in memory |
| 1 | token refresh | ✅ background worker pool | ✅ before each request |
| 1 | models | ✅ catalog plus per-account `fetchAvailableModels` | ⚠️ per-account entitlements not ported |
| 1 | login on a server with no browser | ✅ CLI `-antigravity-login -no-browser`, or management API `auth-url` + callback | ⚠️ CLI only |
| 2 | Codex chat → Responses | ✅ `reasoning_effort` → `reasoning.effort`, json_schema → `text.format` | ✅ |
| 2 | `/v1/responses` passthrough, WebSocket, device-code login | ✅ | ✅ |
| 3 | SSE `[DONE]`, usage in final chunk | ✅ (usage always sent, `include_usage` ignored) | ⚠️ |
| 3 | json_schema on Antigravity, tools, images | ✅ `antigravity_openai_request.go:88-97,177,245` | ✅ |
| 4 | OpenAI-compatible upstreams with alias pools | ✅ | ✅ |
| 4 | fallback to a different model | ❌ (Plus fork: ✅ one hop + one global chain, not nested) | ❌ |
| 4 | `/v1/embeddings`, audio transcription | ❌ | ❌ |
| 4 | plugins | in-process C-ABI `.so` via dlopen, needs cgo and glibc; off by default | same ABI, limited |
| 5 | per-key model restriction | ❌ flat `access.api-keys` (Plus: ✅ config-file whitelist) | ❌ |
| 5 | per-key usage | ⚠️ raw pop-once queue (`/v0/management/usage-queue`) | ⚠️ |
| 5 | management API and UI | ✅ v8 API; panel downloaded from a separate repo | ✅ panel built in |
| 6 | embeddable library | ⚠️ `sdk/cliproxy` embeds the whole service; executors are `internal/` | ⚠️ git-only crates, no stability promise |
| 6 | release pace | 48 releases in 30 days; module-path majors v7 (2026-05-09) and v8 (2026-09-27); MIT; about 55k stars | repo created 2026-10-03; 9 releases; 34 stars; parity 835 covered, 707 partial, 145 missing of 1,687 |
| 8 | footprint | 44.7 MB idle, about 57 MB under load, 69 MB binary | 17.3 MB idle (75 to 530 MB with large prompts), 36 MB binary |
| 8 | persistence | files in `auth-dir`; optional Postgres, git or S3 store | same formats |

Caution: the Codex translator always sets `parallel_tool_calls: true` and drops `max_tokens`
(`codex_openai_request.go:66-81`).

## CLIProxyAPIPlus

A fork (22 stars, effectively one maintainer, 0 commits behind upstream, releases within hours of upstream) that
adds `fallback-models`, `fallback-chain`, `fallback-max-depth`, a per-key whitelist and a `/v1/systemone`
passthrough, with tests. Rejected: every addition is the front door's job in remora, and a single maintainer is a
supply risk. Keep upstream.

## How remora wires it

- Built from source, run as a separate process on 127.0.0.1, its control panel disabled
  (`remote-management.disable-control-panel: true`), management secret random and known only to remora.
- Registered in the front door as one OpenAI-compatible provider with formats completions, responses, messages.
- Plugins off.
- Settings to set: `upstream.codex.model-level-cooling`, `cooldown.save-cooldown-status`, a
  `stream-bootstrap-timeout` if a proxy with a short read timeout sits in front.
- Logins driven by remora's UI through the management API (see the spec and
  [2026-10-14-engine-management-apis.md](2026-10-14-engine-management-apis.md)).

## Terms of service

Using consumer subscriptions through a proxy may conflict with the providers' terms. CLIProxyAPI carries the same
caveat. remora's README states it plainly.
