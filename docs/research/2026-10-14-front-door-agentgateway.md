# agentgateway as the front door

Date: 2026-10-14. Source: `agentgateway/agentgateway` at main `e523afb` (2026-10-13) and tag v1.6.0
(2026-10-02), read, not run. Paths: `ag/` = `crates/agentgateway/src`, `llm/` = `crates/llm/src`.
Verdict: rejected as remora's front door; strong policy layer, but see "cannot own".

Rust, Apache-2.0, a Linux Foundation project (originally Solo.io), about 5.3k stars. Standalone binary with
flat YAML config, or Kubernetes with a controller and Gateway API.

## Capability matrix

### Endpoints

| item | status | evidence |
|---|---|---|
| chat/completions, responses, messages (+count_tokens), embeddings, `/v1/models`, rerank, realtime | ✅ | `llm/lib.rs:152` `RouteType`; `SERVING_ENDPOINTS` in `ag/llm/model_router.rs:67-88` |
| `/v1/audio/transcriptions` | ⚠️ passthrough | `model_router.rs:155`; multipart: only `model` is rewritten, other fields kept (`:1024`); body buffered up to 32 MiB |
| Soniox | ❌ | no translation for a file + job API |
| `/v1/decisions` → SystemOne | ⚠️ main only | PR #3839 merged 2026-10-07, after v1.6.0; inbound `/v1/systemone` answers 501 ("SystemOne is only an upstream route", `httpproxy.rs:3018`) |

### Fidelity

| item | status | evidence |
|---|---|---|
| SSE forwarded byte-faithfully | ✅ | `llm/parse/passthrough.rs`, original frames forwarded unchanged; no synthetic keepalive chunks |
| `response_format`, tools, `reasoning_content`, images, unknown fields | ✅ | loose structs with `#[serde(flatten)]`, `preserve_order` |
| `reasoning_effort` | ⚠️ | typed as the async-openai 0.42 enum; non-standard values may be rejected |
| body unchanged | ⚠️ | re-serialised; `max_tokens` moved to `max_completion_tokens` for `gpt-*` |
| zero-width stripping | ✅ none | no text normalisation in the LLM path |
| limits | ⚠️ | 32 MiB buffers; no default request timeout |

### Routing

| item | status | evidence |
|---|---|---|
| virtual models: weighted, failover, CEL-conditional, HTTP callout | ✅ | `ag/types/local.rs:533-551` |
| failover priority groups | ✅ | eviction automatic with more than one group (`ag/llm/mod.rs:87`); power-of-two-choices inside a group |
| failover of the same in-flight request | ⚠️ | retry re-selects a provider, but `MAX_BUFFERED_BYTES = 64 * 1024` (`httpproxy.rs:1174`): above 64 KiB "initial body is too large to retry". The request fails, the backend is evicted, only the next request fails over |
| 429 triggers failover | ⚠️ | only 5xx and connection errors by default; needs `health.unhealthyExpression` |
| cross-provider, several accounts of one provider | ✅ | |

### Governance

| item | status |
|---|---|
| API keys with metadata, plain or sha256 | ✅ `ag/http/apikey.rs:455` |
| per-key model allowlist | ✅ native `allowedModels` with globs, checked before routing, filters `/v1/models` |
| CEL policy (`llm.requestModel`, `apiKey.<field>`) | ✅ |
| budgets (USD or tokens, rolling windows), persisted in SQLite/Postgres | ✅ |
| cost tracking, runtime key CRUD (`/api/config/resources/llm.apiKey`) | ✅ |

### Providers and extensibility

- Built in: OpenAI, Gemini, Vertex, Anthropic, Bedrock, Azure, Copilot, plus `Custom` with 16 presets (OpenRouter,
  DeepSeek, Groq, Ollama, Typesafe...). Custom = `baseUrl` + a `formats` list.
- OAuth subscriptions: Copilot only.
- CLIProxyAPI fits behind it as a custom backend with `formats: [completions, responses, messages]`.
- Providers are a closed enum (`ag/llm/mod.rs:150`); nothing registers a provider in-process. The SystemOne
  provider took about 1.1k lines across 31 files. Extension points: ext_proc, ext_authz, guardrail webhook, HTTP
  callout, CEL transforms. No WASM.

### Operations

About 93 MB single binary with the UI embedded (`include_dir`); config and secret files watched; stateless unless
SQLite/Postgres is configured; OTel `gen_ai.*` metrics and traces. Rust 1.99, edition 2024, LTO, `aws-lc-sys`
needs cmake and a C toolchain; no `protoc` (pure-Rust `protox`).

## Can own / cannot own

Can own: auth and keys, allowlists, budgets, virtual models, failover after eviction, weighted multi-account
balancing, cost and usage telemetry, faithful SSE, the standard endpoints, CLIProxyAPI as a backend.

Cannot own: same-request failover for large prompts (our agentic and memory prompts are 20k to 98k tokens, well
above 64 KiB), Soniox, `/v1/decisions` on a released tag, in-process provider plugins.

## Why it lost to Bifrost

The decisive point is the 64 KiB replay cap: the first large request to a failing member always fails. Second,
decisions were unreleased. It wins on SSE fidelity, alias-level allowlists, nested virtual models and cross-request
eviction; remora compensates for Bifrost's weaknesses on those points in its own layer (see the spec).
