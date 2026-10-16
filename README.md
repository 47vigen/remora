# remora

An open-source LLM gateway that rides on well-maintained engines instead of rebuilding them, and gives them one
API, one UI and one configuration.

## What it does

- One OpenAI-compatible endpoint for chat, responses, messages, embeddings, speech-to-text and decision models.
- Subscription accounts (Antigravity, Codex, Grok and more) next to API-key providers, pooled.
- Aliases with nested fallback chains: free and subscription capacity first, paid providers when a member answers
  429 or 402.
- Keys per consumer, with allowlists on aliases, usage and logs.
- A small web UI, a management API, and `remora.yaml` import/export.
- One image, one port, one volume.

## Quick start

You need Docker, `curl`, `jq` and `openssl`. CI runs the shell blocks of this section, in order, on every pull
request and before every release, so they work as written.

<!-- quickstart:begin -->

Start remora with an admin password you choose, and wait until it is healthy. The first boot can take up to two
minutes on a slow host.

```sh
export REMORA_ADMIN_PASSWORD="$(openssl rand -hex 16)"
docker run -d --name remora -p 8080:8080 -v remora-data:/data -e REMORA_ADMIN_PASSWORD ghcr.io/47vigen/remora:latest
for _ in $(seq 60); do curl -fsS http://localhost:8080/healthz >/dev/null 2>&1 && break; sleep 2; done
curl -fsS http://localhost:8080/healthz
```

Open <http://localhost:8080> and sign in with that password (`echo "$REMORA_ADMIN_PASSWORD"` shows it). Then:

1. **Providers → Add**: an OpenRouter provider with your OpenRouter key.
2. Optionally, **Providers → a subscription provider → Add account**: sign in with the provider in your browser,
   then paste the final address back into remora. Read
   [Subscription accounts and terms of service](#subscription-accounts-and-terms-of-service) first.
3. **Keys → Create**: a key for your client. Its value is shown once; copy it then.

The same from a shell, with the [management API](api/openapi.yaml): sign in (the session cookie goes to a
temporary file, removed at the end), add the provider, and create a key that may call any OpenRouter model.

```sh
export UPSTREAM_URL=https://openrouter.ai/api UPSTREAM_KEY=sk-or-your-key
COOKIES="$(mktemp)"
jq -n --arg password "$REMORA_ADMIN_PASSWORD" '{password: $password}' |
  curl -fsS -c "$COOKIES" -H 'Content-Type: application/json' -d @- http://localhost:8080/api/auth/login
jq -n --arg url "$UPSTREAM_URL" --arg key "$UPSTREAM_KEY" \
  '{name: "openrouter", kind: "builtin", type: "openrouter", base_url: $url, keys: [{name: "main", value: $key}]}' |
  curl -fsS -b "$COOKIES" -H 'Content-Type: application/json' -d @- http://localhost:8080/api/providers >/dev/null
REMORA_KEY="$(curl -fsS -b "$COOKIES" -H 'Content-Type: application/json' \
  -d '{"name":"my-app","allow":["openrouter/*"]}' http://localhost:8080/api/keys | jq -r .value)"
export REMORA_KEY
rm -f "$COOKIES"
```

Call a model with the new key. Models are named `provider/model`. The engines load a new provider within a few
seconds, so the call is retried a few times.

```sh
curl -fsS --retry 10 --retry-delay 2 --retry-all-errors http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer $REMORA_KEY" -H 'Content-Type: application/json' \
  -d '{"model":"openrouter/openai/gpt-4o-mini","messages":[{"role":"user","content":"Say hi"}]}' |
  jq -r '.choices[0].message.content'
```

<!-- quickstart:end -->

To run remora with Docker Compose instead, use [`deploy/compose.yaml`](deploy/compose.yaml).

## Configuration

remora reads its settings from the environment. Everything else (providers, accounts, aliases, keys) lives in its
store and is managed in the UI, through the management API, or with `remora.yaml`.

| Variable | Default | What it does |
|---|---|---|
| `REMORA_LISTEN` | `:8080` | The address of remora's only listener: `/v1`, `/api`, `/healthz` and the UI. |
| `REMORA_DATA` | `/data` | The data directory. Mount a volume here. |
| `REMORA_ADMIN_PASSWORD` | unset | The admin password, used only while no admin exists. After the first boot the stored password wins; change it in Settings. When unset at first boot, a password is generated and written to the log once. |
| `REMORA_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error`. |
| `REMORA_ENGINE_DIR` | `/usr/local/lib/remora/engines` | Where the engine binaries are. The image sets this up; change it only for a custom build. |
| `REMORA_MASTER_KEY` | unset | The key that encrypts stored provider keys: base64, 32 bytes. When unset, `$REMORA_DATA/master.key` is generated at first boot. Back it up with the volume: without it, stored provider keys cannot be decrypted. |
| `REMORA_PORT_ROUTER` | `18080` | The loopback port of the routing engine. |
| `REMORA_PORT_ACCOUNTS` | `18317` | The loopback port of the subscription engine. |
| `REMORA_PORT_SPEECH` | `18090` | The loopback port of the speech-to-text adapter. |
| `REMORA_SONIOX_BASE_URL` | `https://api.soniox.com` | The Soniox API the speech-to-text adapter calls. |
| `REMORA_SONIOX_POLL_INTERVAL` | `1s` | How often the adapter polls a running Soniox transcription. |
| `REMORA_SONIOX_DEADLINE` | `30m` | How long the adapter waits for one transcription before it gives up. |
| `REMORA_SONIOX_SWEEP_INTERVAL` | `10m` | How often the adapter deletes leftover Soniox files and transcriptions. |
| `REMORA_COOLING_429_COUNT` | `3` | How many 429 answers within the window cool an alias member. |
| `REMORA_COOLING_429_WINDOW` | `60s` | The window for `REMORA_COOLING_429_COUNT`. |
| `REMORA_COOLING_DEFAULT` | `5m` | How long a member cools when the upstream gave no retry hint. |
| `REMORA_COOLING_POLL` | `1s` | How often remora reads the request log for failures. |
| `REMORA_COOLING_SUBSCRIPTION_DEFAULT` | `30s` | How long a subscription account member cools when the upstream gave no retry hint. A retry hint always wins. |
| `REMORA_LOG_CONTENT` | `false` | Keep request and response bodies in the request log. Off by default: the log keeps 7 days of metadata and errors only. |
| `REMORA_PPROF_ADDR` | unset | A loopback `host:port`, such as `127.0.0.1:6060`, that serves Go profiling (`net/http/pprof`) on its own listener. Off when unset; any other host is refused. |

The internal ports are bound to `127.0.0.1` inside the container and are never published. Change them only when
they collide with something else in the container.

The `remora export`, `remora apply` and `remora import` commands talk to a running remora:

| Variable | Default | What it does |
|---|---|---|
| `REMORA_URL` | `http://127.0.0.1` on the port of `REMORA_LISTEN` | The remora to talk to. |
| `REMORA_TOKEN` | unset | An API token (Settings → API tokens). Without one, `REMORA_ADMIN_PASSWORD` signs in. |
| `REMORA_PROVIDER_<PROVIDER>_<KEY>` | none | A provider key's value. `remora.yaml` never holds secrets, only `env:` references with these names; `apply` reads them from the environment. |
| `REMORA_KEY_<NAME>` | none | A client key's value, the same way. |

In the last two names, letters are upper-cased and every other character that is not a letter or digit becomes
`_`: the key `main` of provider `openrouter` is `REMORA_PROVIDER_OPENROUTER_MAIN`.

### The data directory

```
/data/remora.db     remora's store (SQLite), the source of truth
/data/master.key    the generated master key, when REMORA_MASTER_KEY is unset
/data/<engine>/     one directory per engine: its configuration, request logs and subscription account credentials
```

Back up the whole volume, including `master.key`.

## Architecture

```
                         one port (default 8080)
client ──► remora ──┬── /v1/*  inference proxy ──► routing engine (127.0.0.1) ──┬── API-key providers (OpenRouter, ...)
                    │                              see Credits                  ├── subscription engine (127.0.0.1) ──► Antigravity, Codex, Grok, ...
                    │                                                           │   see Credits
                    │                                                           └── speech-to-text adapter (127.0.0.1, inside remora) ──► Soniox
                    ├── /api/*  management API (OpenAPI)
                    └── /       web UI (embedded)
```

remora is one image with one exposed port. Inside it, remora supervises the engines as child processes bound to
loopback ports, writes their configuration from its own store, and restarts them when they crash. Clients and
admins only ever talk to remora: one key format, one error format, one API, one UI.

## Subscription accounts and terms of service

Using consumer subscriptions through a proxy may conflict with the terms of service of the companies that offer
them. Read those terms and decide for yourself; remora does not change them.

## Dropped parameters

Codex models served through a subscription do not accept `temperature`, `top_p` or `max_tokens`; those
parameters are dropped, and remora says so: the `x-remora-dropped-params` response header lists every top-level
request key the Codex backend did not receive as sent. It is absent when nothing was dropped or when any other
provider served the request.

remora announces every request rewrite it knows of; none is silent. When a Codex member (`x-remora-served-by`
starts with `codex/`) serves a request, the header is a comma-separated, sorted list. It is set on streaming and
non-streaming answers alike, but not on the Responses WebSocket route, where it would have to be sent per message.

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

The list is checked against the engine on every engine update by contract tests. What remora does and does not do,
each row with the test that proves it, is in [`docs/parity.md`](docs/parity.md).

## Thinking level

A subscription model in an alias may carry an explicit thinking level as a suffix: `(minimal)`, `(low)`,
`(medium)`, `(high)`, or a token budget such as `(1024)`, where `(0)` turns thinking off. For example, an alias
`flash-fast` with the member `antigravity/gemini-3-flash(low)` answers faster than one without the suffix.

- The suffix applies only to subscription models; the alias API refuses it on any other provider.
- For that member it overrides the client's `reasoning_effort`. A model without thinking ignores it.
- No existing alias changes, and there is no default level: a faster tier is a separate, named alias.

## Verifying an image

Every release image is signed with cosign (keyless, by the release workflow) and carries an SBOM and build
provenance. Verify a digest with:

```sh
cosign verify ghcr.io/47vigen/remora@<digest> \
  --certificate-identity-regexp '^https://github\.com/47vigen/remora/\.github/workflows/release\.yml@refs/' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

## Documentation

The design, the plans, the research and the maintainer notes are indexed in [`docs/README.md`](docs/README.md).

## Credits

remora composes two engines, and never forks them for long:

- [Bifrost](https://github.com/maximhq/bifrost) by Maxim AI (Apache-2.0) is the routing engine: inference routing
  and same-request fallback across providers, API-key providers, embeddings, transcription transport, decision
  models, and the request logs and analytics behind remora's logs and stats.
- [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) (MIT) is the subscription engine: OAuth sign-in for
  subscription accounts, account pooling, token refresh, and per-account cooldown and quota.

Their licenses and notices are in [`THIRD_PARTY_NOTICES.md`](THIRD_PARTY_NOTICES.md). Every change remora carries
on top of an engine, with its upstream pull request, is in [`docs/patches.md`](docs/patches.md).

## License

[Apache-2.0](LICENSE).
