# The reference deployment: what remora must replace

Date: 2026-10-14. Source: the live database and request logs of a production OmniRoute 3.8.51 gateway
(7 days of `call_logs`, 2026-10-07 to 10-14, 58,166 rows; about 10k stored request bodies), plus the code of
every service that calls it. Everything was read-only. Service names below are generic on purpose.

This is the workload remora 0.1 is sized and tested against. "Parity" in the spec means this document.

## The gateway being replaced

- OmniRoute 3.8.51 (Next.js, Node), one container with Redis and nginx, on a 2 vCPU / 4 GB VM.
- Container at rest: about 1.06 GB RAM, 3% CPU. `storage.sqlite` is 490 MB with about 160 tables, most for
  features nobody uses here (gamification, badges, A2A, memory, semantic cache). Request logs keep 7 days.
- About 8k requests a day, bursty (one consumer goes from 12 to 4,815 a day).

### Measured gateway overhead (on the host, loopback)

| call | result |
|---|---|
| `GET /v1/models` cold | 3.8 s, 1.49 MB (about 4,000 models) |
| `GET /v1/models` warm | 17 ms |
| tiny chat, a GPT-6 Luna combo (Codex OAuth) | TTFB 1.3 to 1.4 s |
| tiny chat, a Gemini Flash combo (Antigravity OAuth) | TTFB 2.2 to 4.0 s |
| login page | 60 ms |

Conclusion: the gateway adds milliseconds; latency is upstream. The pain is weight, operability and bugs, not
the request path. remora is not justified by raw speed. It is justified by being small, correct and maintainable
on top of engines that are maintained by others.

## Upstreams in use

| upstream | auth | share of 7-day traffic | notes |
|---|---|---|---|
| Google Antigravity | OAuth, 3 Google accounts pooled | about 55% | Gemini 3.8 Flash tiered, Gemini 3.7 Flash, Claude Opus 4.6 thinking |
| OpenRouter | API key | about 23% | almost all of it embeddings |
| ChatGPT Codex | OAuth, 1 account | about 4% | GPT-6 Sol / Luna, GPT-5.6 Luna; chat translated to the Responses API |
| Soniox | API key | small | async speech-to-text |
| Grok CLI | OAuth | idle | |
| Cline | OAuth | idle | out of credit (402) |
| opencode, opencode-go | API key | idle this week | DeepSeek V4.1 Flash, Qwen, Muse |
| three OpenAI-compatible nodes (Experiential, TokenHarbor, KiraAI) | API key | idle or failing | Experiential's key was revoked: 665 of 665 calls failed with 429 |

## Consumers

| consumer | endpoints | features | volume/day |
|---|---|---|---|
| memory service | `/v1/chat/completions` (about 830), `/v1/embeddings` (about 1,870) | `json_schema` (237 of 508 bodies), tools with `tool_choice:auto` and replayed tool history, streaming with `stream_options`, `max_tokens` and `max_output_tokens`, embeddings with `dimensions: 1536` and batched input; largest prompt 98k tokens | about 2,700 |
| product matcher | `/v1/chat/completions` | not streamed, `temperature`, `max_tokens`; sends `json_schema` only if `/v1/models` lists `structured_outputs` in `supported_parameters`; retries a fallback model once on its own side | about 3,000 |
| web crawler | `/v1/chat/completions` | `stream:false`, `response_format:{type:text}`, prompts under 1k tokens, up to about 3k reasoning tokens | about 1,500 |
| catalog miner | `/v1/chat/completions` | `json_schema` (318 of 458 bodies), `temperature`, `max_tokens`; model chain per task stored in its own DB | about 820 average, very bursty |
| voice-call analyser | `/v1/audio/transcriptions`, `/v1/chat/completions` | multipart `file`, `model=soniox/stt-async-v5`, `enable_speaker_diarization=true`, `context`; reads `text`, `language`, `duration`, `segments[]{id,speaker,start,end,text}` and fails without segments; 10-minute client timeout. Chat: strict `json_schema`; a streamed tool-calling agent through `openai-go` | low, idle this week |

Configured but idle since September: a coding-agent orchestrator (needed `/v1/responses`, WebSocket and a
context window over 200k), Claude Code combos (Anthropic `/v1/messages`), and nine client keys.

### Fallback chains in use

Combos nest. Example, the memory service's fast path:

```
memory/fast -> gemini-flash-latest -> gemini-3.8-flash -> antigravity/gemini-3.8-flash-tiered
            -> gpt-6-luna -> codex/gpt-6-luna
```

The serving model must be visible in the response `model` field.

## MUST

1. `/v1/chat/completions`, streamed and not; clean SSE ending with `[DONE]`; `usage` (with `reasoning_tokens`) in
   the final chunk; no synthetic chunks with a different `id`.
2. Strict `response_format: json_schema` honoured on every chain member, unfenced. Also `type: text`.
3. Tools: `tool_choice: auto`, multi-turn tool history, tool calls inside streams.
4. `/v1/embeddings` with `dimensions`, batched input, `encoding_format: base64` (the openai-python default); the
   same embedding model as today, or the consumer's whole vector store must be re-embedded.
5. `/v1/audio/transcriptions` in front of Soniox's async API: diarization, `context`, verbose response with
   `segments` and `speaker`, multi-minute jobs, and deletion of the Soniox file and transcription after every job.
   Unknown parameters must never be dropped silently.
6. Named aliases with ordered, nestable fallback chains across different models and providers; 429 (including a
   free tier's `insufficient_quota`) and 402 trigger fallback; fail fast.
7. Upstreams: Antigravity OAuth (several accounts pooled), Codex OAuth (chat to Responses translation), OpenRouter,
   Soniox, OpenAI-compatible providers; Grok, Cline, opencode as configured today.
8. One key per consumer; a model allowlist per key checked against the alias the client asked for; usage and
   logs per key.
9. `/v1/models` listing our aliases only, with `supported_parameters` including `structured_outputs`.
10. Both `max_tokens` and `max_output_tokens`; reasoning effort passed through.
11. Decision models: `/v1/systemone` (TypeSafe format) and `/v1/decisions` (OpenAI format), free promotional
    provider first, OpenRouter on 429.
12. No global single-heavy-request admission; prompts up to about 1M tokens; long streams without short queue
    deadlines; per-model context-window overrides.

## MUST NOT (observed OmniRoute behaviour)

- Strip U+200B, U+200C, U+200D from responses. It breaks the Persian half-space (ZWNJ): «می‌شود» comes back as
  «میشود». Verified with a minimal reproduction; U+00AD survived, so the model can emit invisibles.
- Send a keepalive chunk with its own id (`chatcmpl-keepalive`, empty delta). `openai-go`'s accumulator locks onto
  the first id and silently discards the whole real answer.
- Drop unknown multipart parameters silently (a missing diarization then looks like success).
- Ignore aliases on `/v1/embeddings`.
- Check key allowlists against the resolved member instead of the alias (and let the panel's Test button bypass
  the check).
- Cap context at 200k from a catalog without a per-model override.
- Return 504 to a non-streamed huge prompt because of a 15 s queue deadline.
- Admit one heavy request at a time server-wide and shed the rest with 503 `chat_admission_busy`.
- Cool down every connection of a provider because one member failed. Observed: 7,579 failed calls from one
  consumer in a week, about 140 ms each, "all targets were skipped by pre-dispatch filters".
- Hang 76 to 100 s on a failing combo instead of failing fast.
- Never delete Soniox files (hit `Total file count limit` once; Soniox keeps 1,000 files and deletes leftovers only
  after 30 days).
- Drop the `agent_message` input item type on `/v1/responses`.
- Return about 4,000 models from `/v1/models`.
- Keep routing to members that are out of credit (402) or capped (429).
- Run on one SQLite file that cannot be clustered, with OAuth tokens that cannot be shared across instances.
- Not log audio requests at all.
