# Decision models

Date: 2026-10-14. Sources: [OpenRouter decision rankings](https://openrouter.ai/rankings/decisions) (usage through
2026-10-13, CC BY 4.0), OpenRouter's model API (`/api/v1/models?output_modalities=decisions`, 22 models), and the
Experiential platform reference (`platform.experientiallabs.ai/llms.txt`).

## What they are

Not chat. A request carries a `state` (text, JSON, sometimes images or audio) and typed questions; the model
returns calibrated probabilities instead of text. Two wire formats:

- **System One** (TypeSafe's format): `POST /v1/systemone`, question types `noul` (yes/no), `choice` (one of a
  criteria map), `score` (ordered levels). Served by Jev, Perplexity Decider, Cloudflare Clef, Inception Mercury
  Decide, Liquid d1, Upstage Solar Decide and others.
- **OpenAI Decisions**: `POST /v1/decisions`, `input` plus `questions[]` (e.g. `type: predicate`), answers as an
  array. Served by GPT-6 Luna Decisions.

Output tokens are free on every listed model; prices below are per million input tokens.

## Weekly leaderboard (requests, OpenRouter, week to 2026-10-13)

| rank | model | requests | change |
|---|---|---|---|
| 1 | typesafe/jev-1.13 | 1.06B | +8% |
| 2 | perplexity/pplx-decider-v1.1-27b | 30.6M | new |
| 3 | openai/gpt-6-luna-decisions | 24M | >999% |
| 4 | nace-ai/drex-v1.5 | 18.7M | new |
| 5 | microsoft/microsoft-decision-1 | 13.8M | new |
| 6 | cloudflare/clef-flash | 11.9M | +399% |
| 7 | inception/mercury-decide:free | 10.7M | >999% |
| 8 | cloudflare/clef | 8.02M | +345% |
| 9 | inception/mercury-decide | 7.54M | >999% |
| 10 | nace-ai/drex-v2.0 | 4.92M | new |

## Catalogue

| model | $/M input | context | input | note |
|---|---|---|---|---|
| typesafe/jev-1.13 | 0.042 | 64k | text | the category leader |
| perplexity/pplx-decider-v1.1-27b | 0.02 | 262k | text, image | |
| openai/gpt-6-luna-decisions | 0.10 | 1.05M | text, image | OpenAI Decisions format; web search $0.01 |
| nace-ai/drex-v1.5 / v2.0 | 0.04 / 0.06 | 131k | text | 9B / 35B MoE (3B active) |
| microsoft/microsoft-decision-1 | 0.042 | 32k | text | |
| cloudflare/clef-flash / clef | 0.021 / 0.042 | 24k / 65k | text, image | open weights (Qwen fine-tunes) |
| cloudflare/clef-omni | 0.15 | 65k | text, image, audio, video | the only one taking audio |
| inception/mercury-decide (`:free`) | 0.02 (free) | 65k | text, image | |
| liquid/d1 | 0.02 | 65k | text | |
| upstage/solar-decide / -flash | 0.05 | 524k | text | |
| scaledown/classify-1 | 0.04 | 1M | text, image, file | |
| togethercomputer/tev1-4b-experimental | 0.042 | 32k | text | |
| respan/span-01 (`-lite`, `:free`) | 0.02 / free | | text | behaviour scoring over a conversation span |
| jaredpalmer/kev-4b | 0.042 | 8k | text | open weights, System One contract |

## Free promotional access

Experiential (an OpenAI-compatible hosted gateway) lists Clef Flash, Clef, GPT-6 Luna Decisions and Jev as free
promotional models and serves them on `POST https://api.experientiallabs.ai/v1/systemone` with the same key. Its
gateway bounds: 32 questions, 64 choice options, 2 to 10 score criteria, 262,144 input JSON bytes, about 32k tokens
shared by state and questions for Jev, no automatic retries. When the free allowance is spent it answers 429
`insufficient_quota`.

## What remora does with this

Free first, paid second: an alias such as `jev` resolves to Experiential's System One endpoint, then OpenRouter's
(`https://openrouter.ai/api/v1/systemone`, TypeSafe-compatible). A 429 moves the request to the next member. Both
`/v1/systemone` and `/v1/decisions` are exposed.
