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

## License

[Apache-2.0](LICENSE).
