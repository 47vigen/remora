# The gaps: voice, embeddings, decisions, and the rest of the field

Date: 2026-10-14. Web research plus reading the repos named below.

## Voice: Soniox

- Soniox has no OpenAI-compatible transcription endpoint. Its API is async: upload a file, create a
  transcription, poll, fetch the transcript ([API reference](https://soniox.com/docs/stt/api-reference)). Its
  "OpenAI-compatible" Omnio surface is chat, not transcription.
- Deleting is a correctness requirement: defaults are 1,000 stored files, 10 GB and 2,000 transcriptions, and
  "You must manually delete files"; leftovers are auto-deleted only after 30 days
  ([limits](https://soniox.com/docs/stt/async/limits-and-quotas.mdx)).
- LiteLLM has a native provider (`litellm/llms/soniox/`, about 1,100 lines). It uploads, creates, polls (up to 30
  minutes, intervals clamped), fetches, and deletes file and transcription in a `finally` block. But its
  `verbose_json` returns only `words[]{word,start,end}` with no speaker, and no `segments`; speakers only appear as
  "Speaker N:" text inside `text`. It sends `enable_speaker_diarization` as the string `"true"` from multipart.
  Real-time streaming is an open issue.
- No other gateway (Bifrost, agentgateway, Portkey, Envoy AI Gateway) has Soniox.

Decision: remora ships its own adapter (about 300 lines of Go) behind the front door as a custom provider. It uses
LiteLLM's behaviour as a reference (delete in `finally`, clamped polling, boolean parsing, secret redaction) and
adds what nobody has: `segments[]` with `speaker`, grouped from Soniox tokens, plus a periodic sweep of leftover
files and transcriptions so a crash between fetch and delete cannot fill the quota.

## Embeddings

- Bifrost: embeddings across its providers, OpenRouter included; `dimensions` and `encoding_format` passed;
  aliases and fallbacks apply.
- agentgateway: passes the upstream body byte for byte, but parses the response into a struct that requires
  `object` and `model`.
- Gotcha: the openai-python SDK sends `encoding_format: "base64"` by default, and OpenRouter's embeddings docs
  document neither base64 nor `dimensions`. Test that vector width and values are unchanged before switching a
  consumer.

## Decision models

See [2026-10-14-decision-models.md](2026-10-14-decision-models.md) for the models. Gateway support on 2026-10-14:

| gateway | `/v1/decisions` | `/v1/systemone` | cross-provider fallback |
|---|---|---|---|
| Bifrost | ✅ released | ✅ `/typesafe/v1/systemone` | ✅ with a custom TypeSafe base URL |
| agentgateway | ✅ main only (after v1.6.0) | ❌ inbound 501 | unverified |
| LiteLLM | ✅ | ✅ since v1.104.2 | ✅ router fallbacks |
| OmniRoute | ❌ (open PRs and issues only) | ❌ | |
| CLIProxyAPI | ❌ (issue #6012 closed without a plan) | community `.so` plugins | |

Experiential's own provider "waterfall" fails over only on capacity and transport errors. When its free promotion
is used up it answers 429 `insufficient_quota`; the only way past that inside Experiential is billing the overflow
to credits. So the gateway in front must treat that 429 as the fallback trigger.

## Front-door candidates

| engine | language | stars | latest release | verdict |
|---|---|---|---|---|
| Bifrost | Go | 8.8k | transports v2.3.0, 2026-10-13 | chosen |
| agentgateway | Rust | 5.3k | v1.6.0, 2026-10-02 | strong, rejected (64 KiB replay cap, decisions unreleased) |
| LiteLLM (Python) | Python | 61k | v1.105.1, 2026-10-14 | feature leader, but needs Postgres for keys, heavy, no Antigravity |
| LiteLLM Rust mode | Rust (beta) | | | chat/messages/responses/OCR/transcription only; no embeddings, no fallbacks, keys only active/revoked |
| Helicone ai-gateway | Rust | 636 | none; last push 2025-11-21 | stale |
| TensorZero | Rust | 11.7k | archived | out |
| Portkey gateway | TypeScript | 13.2k | v1.15.2, 2026-01-12 | not Go/Rust, slowing |
| Envoy AI Gateway | Go | 2.2k | v1.2.0, 2026-10-07 | Kubernetes/Envoy-heavy; no decisions, no Soniox |

### LiteLLM in detail (rejected for every slot)

Per-key allowlists exist but Postgres is mandatory for keys (`schema.prisma`). Fallbacks nest (up to 5 hops) and
any error except context-window or content-policy triggers them; 429/401/402/404/408 cool down the deployment.
`/v1/models` has no `supported_parameters`. No Antigravity. As a sidecar for two endpoints it brings far too much
weight.
