# Bifrost as the front door

Date: 2026-10-14. Source: `maximhq/bifrost` branch `dev` at `72457fe` (2026-10-14); decisions routes present in
`core/v1.12.0` and `transports/v2.3.0`. Read, not run. Verdict: chosen as remora's front door.

Go, Apache-2.0, about 8.8k stars, at least 5 gateway releases in the last 30 days. Docker image about 86 MB.
Building from source needs Node (the UI is embedded with `//go:embed all:ui`) and CGO for SQLite.

## Checklist against the reference deployment

| # | status | evidence | note |
|---|---|---|---|
| 1 chat and SSE | ⚠️ | `core/providers/openai/openai.go:1475-1960`; `transports/bifrost-http/lib/streamreader.go:140` | `[DONE]` sent, usage forced on. Every chunk is parsed and rewritten, so SSE is not byte-faithful; the final added chunk reuses the upstream id |
| 2 json_schema | ✅ | `core/providers/openai/chat.go:206-214` | passed unfiltered to OpenAI-type custom providers |
| 3 tools | ✅ | standard chat schema | |
| 4 embeddings | ✅ | `core/schemas/embedding.go:295-296,400`; `core/providers/openrouter/openrouter.go:451` | `dimensions` and `encoding_format` passed; base64 stays a string; aliases and fallbacks apply (`core/bifrost.go:5680`) |
| 5 transcription | ⚠️ | `transports/bifrost-http/handlers/inference.go:1846-1910`; `core/providers/openai/transcription.go:105` | unknown multipart fields forwarded, but `TranscriptionSegment` has no `speaker` (`core/schemas/transcriptions.go:213`); no Soniox |
| 6 aliases and fallbacks | ⚠️ | `core/bifrost.go:5596-5800`; `plugins/routing/main.go:403,543` | see below |
| 7 upstreams | ✅/⚠️ | `docs/providers/custom-providers.mdx:248-260`; `core/network/ssrf.go:444` | OpenRouter built in; custom OpenAI-type providers; `/v1/responses` passthrough; a 127.0.0.1 upstream may need `allow_private_network` |
| 8 keys | ⚠️ | `docs/features/governance/virtual-keys.mdx:351`; `plugins/governance/main.go:140,1110` | virtual keys with budgets, rate limits, logs; the allowlist checks the model after routing, not the alias |
| 9 `/v1/models` | ⚠️ | `core/schemas/models.go:179`; `framework/modelcatalog/datasheet/store.go:19,343` | `supported_parameters` from a datasheet; our aliases need our own file via `model_parameters_url` |
| 10 max tokens, reasoning | ✅ | `core/providers/openai/types.go:451`; `core/schemas/responses.go:240` | |
| 11 decisions | ✅ | `transports/bifrost-http/handlers/inference.go:884`; `integrations/typesafe.go:34`; `core/providers/typesafe/typesafe.go:53` | `/v1/decisions` and `/typesafe/v1/systemone`; a custom TypeSafe provider takes a base URL (Experiential); OpenRouter via a second custom TypeSafe provider with base `https://openrouter.ai/api` |
| 12 concurrency | ✅ | `core/schemas/provider.go:13-21` | per-provider queues (1000 concurrent, 5000 buffered); 300 s request timeout, 120 s stream idle, 100 MB body; no global admission |

| MUST NOT | status | note |
|---|---|---|
| strip zero-width characters | ✅ | no text normalisation |
| keepalive with a different id | ✅ | heartbeat is an SSE comment (`: heartbeat`) |
| one error cools down a whole provider | ✅ | no cross-request cooldown in the open-source build |
| keep routing to 402/429 members | ❌ | failed keys are remembered for one request only; cross-request avoidance ("Adaptive Load Balancing") is Enterprise |
| embeddings ignore aliases | ✅ | |

## Fallbacks in detail

- One in-flight request can move to a different provider and model. No replay body cap: the parsed request stays
  in memory.
- Any error triggers the next fallback except cancellation, a plugin's no-fallback mark, or a malformed request.
  402 and `insufficient_quota` are "quota" failures, 429 is "rate_limit". With `max_retries: 0` a 429 goes straight
  to the next fallback.
- Streams fail over only before the first token, or on the TTFT cutoff.
- No nested aliases: routing rules run once per request, so a chain is a flat list of provider/model pairs.
- The response `model` is what the upstream returned; `x-bifrost-routing-info-*` headers add `is_fallback`.

## Storage and management

- Virtual keys: config.json, UI, or REST (`/api/governance/virtual-keys`). Values must start with `sk-bf-`
  (`plugins/governance/main.go:33`); a config value without it is replaced by a random one. Only config.json lets
  the caller choose the value. Stored plaintext or AES-encrypted, with a SHA-256 lookup hash.
- Config store: SQLite or Postgres, effectively required (governance, routing and config handlers refuse to start
  without it).
- Unknown JSON parameters on chat, responses and embeddings are dropped unless the client sends
  `x-bf-passthrough-extra-params: true`.
- Full management API inventory: [2026-10-14-engine-management-apis.md](2026-10-14-engine-management-apis.md).

## Footprint

Idle memory undocumented. Their sample benchmark shows about 189 MB average and 257 MB peak under load; their
5k RPS benchmark on a t3.medium peaks at 1.3 GB. 1,220 open issues.

## Why it won

Same-request failover with no body cap; 402/429 trigger fallback by default; decisions released; Soniox attaches
as a custom provider without a fork. Its weaknesses (re-serialised SSE, allowlist on resolved model, flat chains,
per-request-only dead-key memory, missing `speaker`) are each handled in the spec: a live fidelity test, an
allowlist and alias compiler in remora, and a one-field upstream patch.
