# remora

An open-source LLM gateway that rides on well-maintained engines instead of rebuilding them, and gives them one
API, one UI and one configuration.

> **Status: design.** Nothing to run yet. Read the [0.1 design](docs/specs/2026-10-14-remora-0.1-design.md) and
> the [research](docs/research/README.md) behind it.

## What it will do

- One OpenAI-compatible endpoint for chat, responses, messages, embeddings, speech-to-text and decision models.
- Subscription accounts (Antigravity, Codex, Grok and more) next to API-key providers, pooled.
- Aliases with nested fallback chains: free and subscription capacity first, paid providers when a member answers
  429 or 402.
- Keys per consumer, with allowlists on aliases, usage and logs.
- A small web UI, a management API, and `remora.yaml` import/export.
- One image, one port, one volume.

## Built on

remora composes, and never forks for long:

- [Bifrost](https://github.com/maximhq/bifrost) by Maxim AI (Apache-2.0): routing, fallbacks, API-key providers,
  logs.
- [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) (MIT): subscription OAuth accounts.

## A word on subscription accounts

Using consumer subscriptions through a proxy may conflict with the terms of service of the companies that offer
them. Read those terms and decide for yourself; remora does not change them.

Codex models served through a subscription do not accept `temperature`, `top_p` or `max_tokens`; those
parameters are dropped, and remora says so in an `x-remora-dropped-params` response header.

## Known engine rewrites

remora announces every request rewrite it knows of; none is silent. Today there is one: the Codex subscription
translator builds its own request and leaves out parameters the Codex backend does not take. When a Codex member
(`x-remora-served-by` starts with `codex/`) serves a request that carried any of them, the response has an
`x-remora-dropped-params` header: a comma-separated, sorted list of the top-level request keys Codex did not
receive as sent. The header is absent when nothing was dropped or when any other provider served the request. It
is set on streaming and non-streaming answers alike, but not on the Responses WebSocket route, where it would have
to be sent per message.

- **`/v1/chat/completions`**: every key except `model`, `messages`, `stream`, `stream_options`,
  `reasoning_effort`, `response_format`, `text`, `tool_choice`, `tools`, `service_tier` and
  `parallel_tool_calls`. So `temperature`, `top_p`, `top_k`, `max_tokens`, `max_completion_tokens`, `stop`, `user`,
  `n`, `seed` and the rest are all reported. Also `service_tier` unless it is `priority`, `fast` or `ultrafast`
  (any case), and `parallel_tool_calls` unless it is `true`, because Codex always runs tool calls in parallel.
- **`/v1/responses`**: `max_output_tokens`, `max_completion_tokens`, `temperature`, `top_p`, `truncation`,
  `prompt_cache_options`, `prompt_cache_retention`, `user` and `context_management`; `service_tier` unless it is
  `priority`, `fast` or `ultrafast` (any case); `parallel_tool_calls` unless it is `true`; and `store` unless it
  is `false`.
- **`/v1/messages`**: `temperature`, `top_p` and `max_tokens`.
- Any other endpoint: nothing.

The header names exactly what Codex did not receive. The list is checked against the engine on every engine
bump by the contract tests in `internal/engine/cliproxyapi/contract_test.go`.

## License

[Apache-2.0](LICENSE).
