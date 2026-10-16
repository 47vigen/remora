# remora performance: design

Date: 2026-10-16. Status: proposed.

## Intent

Raw performance is the main reason remora exists. The target is one node serving a pool of about 20 subscription
(Antigravity) accounts under heavy concurrent load, mostly streamed chat. remora should hold its latency flat from
one request up to hundreds at once, and it should never trade output quality for speed.

A benchmark against the previous gateway showed that remora's own overhead is already small (about 14 ms, most of it
network and TLS, against a 2.3 s model call). Shaving the gateway alone therefore changes almost nothing the client
feels. This design attacks three things together:

1. **Upstream time.** Thinking budget, connection reuse to the provider, and prompt-cache affinity across accounts.
   This is where the seconds are.
2. **Cost per stream.** CPU, garbage and disk per request. This decides how many concurrent streams one box carries
   and whether p99 stays flat.
3. **Failure absorption.** A blip on one account, or a short capacity spike, must not become a client error, a
   cut stream, or a silent switch to another model.

## Evidence

Five read-only audits (gateway hot path, front door, OAuth engine, runtime and load, failure behaviour), plus a
local benchmark of the full chain: remora, front door, OAuth engine, and a fake upstream with scripted latency and
streaming. Measured on an 11-core laptop, so the ratios carry over and the absolute numbers do not.

- **Latency each hop adds**, at 10 concurrent: remora 0.16 ms, front door about 0.9 ms, OAuth engine about 0.5 ms.
  The whole chain adds 1.6 ms p50 and 4.9 ms p99.
- **SQLite idle pool of 2:** above 2 concurrent requests, every `/v1` call opens a new SQLite connection. That was
  49% of remora's CPU. A one-line fix gave 2.2× requests per second, and p99 time to first token at 500 streams
  dropped from 167 ms to 60 ms.
- **Cooling tail:** every second it re-reads the whole 10-minute window of the front door's request log, prompt
  bodies included. This is the only cost that grows faster than traffic, and past about 10–20 requests per second a
  poll outlasts its 1 s tick.
- **Front-door request log:** it keeps full content for 365 days. In the benchmark it reached 3.7 GB after 670k
  tiny requests, and real 50–300 KB prompts would fill a VPS disk within days.
- **OAuth engine:** remora sets only four of its config keys, so the rest run at Go's zero values, not at the
  example config's values.
  - Every request opens a new TCP+TLS connection to the provider, because its connection pool is off.
  - No thinking budget is set.
  - Any 5xx benches an account for 60 s, and the 503 capacity error affects the whole model, so a single spike can
    drain all 20 accounts for a minute.
  - Streams that fail before their first chunk are not retried.
- **Session id:** remora derives it from the first message (`internal/proxy/session.go`), which is usually a shared
  system prompt. Feeding that id to account affinity would pin every client to one account.
- **Request body:** parsed two or three times per request. For a 1 MB agentic body that is 6.4 ms of CPU and 15 MB
  of garbage. Bodies are also held in memory for the whole stream.
- **Fallback chain:** round-robin aliases list every member as a fallback, the chosen member included, and the front
  door falls back even on caller errors (400/413/422).
- **Engine detail leaks:** non-streamed responses reach the client with the front door's `extra_fields`, which
  include the internal provider key id, and with an `X-Cpa-Trace-Id` header. Both break the rule in `AGENTS.md`.
- **Cost per stream:** about 0.3 cores per 100 streams on the laptop, which suggests 0.5–0.8 vCPU per 100 streams
  on a VPS. The front door is the ceiling, and uses about 1 GB of RSS.

## Goals

- Latency added by the gateway stays flat from 1 to 500 concurrent streams, measured by a harness in the repo.
- Time to first token on the subscription pool drops, through connection reuse, prompt-cache affinity and an
  explicit thinking level, without any silent quality loss.
- A capacity spike, or a single account's error, is absorbed in place. The client gets the model it asked for, a
  little later, rather than an error or another model.
- Disk and memory stay bounded however long the node runs.

## Non-goals

- **Redis or any external store.** remora is one process: a loopback Redis round trip (30–100 µs) is slower than
  today's SQLite lookup, and slower still than an in-memory snapshot (164 ns). Nothing here needs state shared
  between nodes.
- **Response or semantic caching of model output.** Exact-match caching almost never hits. Semantic caching returns
  another prompt's answer, which directly breaks the quality goal. The cache that helps is the provider's own prompt
  cache, which the affinity change below serves.
- **Unix sockets or HTTP/2 between processes.** Neither engine supports them without a fork patch, and loopback TCP
  costs microseconds.
- **Bypassing the front door for subscription traffic.** That would mean re-building member choice, fallbacks, logs,
  stats and cooling in remora for about 1 ms. Revisit only if a profile taken after this work shows the front door's
  per-chunk CPU is the ceiling.
- **Request hedging, for now.** Each hedge spends a full prompt's quota on a second account with no refund. It
  doesn't shorten thinking time, and under load it drains the pool faster. Revisit once per-account time-to-first-token
  data shows a fat tail that affinity and connection reuse have not fixed.

## Anti-goals

- Any change that silently serves a different model, a smaller thinking budget, or a truncated stream.
- Any change to the OAuth engine's HTTP fingerprint toward the provider (it keeps HTTP/1.1 on purpose).
- Engine names, keys or ids reaching a client.

## Design

Three pull requests in order. Each is useful alone, and each is measured with the harness that PR 1 lands first,
against a 0.2.0 baseline taken with the same harness.

### PR 1: Harness, configuration and one-line fixes (effort S–M)

The load harness lands first (its contents are under PR 3's old heading, "Load harness in the repo" below), so the
0.2.0 baseline and every later PR are measured the same way.

Every OAuth engine key below goes in both places that render it, `internal/supervise/cliproxyapi.go` (boot) and
`settingsPatch` in `internal/accounts/reconcile.go` (runtime), and in both sets of tests.

remora:
- `internal/store/store.go`: raise `SetMaxIdleConns` to match `SetMaxOpenConns`, and set `SetConnMaxIdleTime(0)`.
- `internal/proxy/proxy.go`: transport `MaxIdleConnsPerHost` and `MaxIdleConns` to 1024.
- `cmd/remora/serve.go` `newServer`: add `IdleTimeout: 120s`.
- An opt-in pprof endpoint on loopback only, `REMORA_PPROF_ADDR`, off by default.
- `internal/supervise`: pass `GOMEMLIMIT` to each child process. Each gets a share of the container's memory limit,
  read from cgroup: front door 50%, OAuth engine 15%, remora's own 10% set at startup. Skip it when no limit is set.

Cooling tail:
- `internal/engine/bifrost/tail.go`: cooling polls with `Status: ["error"]`, which an existing index covers. The live
  log viewer gets its own unfiltered poll, running only while a subscriber is attached.
- Cooling uses only 402/429 rows, so before shrinking the lookback, check whether the front door writes a row when a
  request starts or when it ends.

Front door (`internal/supervise/bifrost.go`):
- `logs_store.retention_days` and `client.log_retention_days`: 7.
- Content logging: off by default. A boot-time environment variable turns it on; there is no API or console switch,
  which keeps this PR small. The console's log detail shows "content
  not logged" when it is off.
- `logs_store.writer.batch_interval`: 1s, so cooling reacts in about 2 s instead of 5–7 s.
- The telemetry plugin off: nothing scrapes it, and it runs a goroutine per stream chunk.

Fallbacks (`internal/reconcile/desired.go`):
- A round-robin alias's fallback chain leaves out the member the rule picked, and stops after 3 entries.

OAuth engine:
- `antigravity.connection-pool`: `{enabled: true, idle-conn-timeout: 90s, max-idle-conns-per-host: 8}`. HTTP/2 stays
  off.
- `transient-error-cooldown-seconds: 5`.
- `request-retry: 1` and `max-retry-interval: 10`, so a pool drained for a few seconds waits, then serves the model
  that was asked for. Before setting it, confirm in the engine that a retry happens only before the first byte goes
  to the client. If it can retry after that, leave it unset: a spliced stream is worse than an error.
- `requests.streaming.bootstrap-retries: 1`.
- `streaming.keepalive-seconds: 15`, so long thinking is not cut by the front door's 300 s idle limit.
- Raise the `remora-oauth` provider's request timeout to 30 minutes, for non-streamed thinking. Other providers keep
  theirs.

remora cooling:
- For a subscription member, a 429 that carries no retry hint cools for 30 s instead of 5 minutes, because the
  accounts behind the member come back within seconds.

### PR 2: Hot path in code (effort M)

- **In-memory snapshot of keys and aliases.** An `atomic.Pointer` rebuilt after every `Store.Write` commit and after
  every reconcile pass. `authenticate` and `checkAliasKind` read it, so no request touches SQLite. Every write to
  these tables already goes through `Write`: `apply` and import use the HTTP API.
- **One pass over the body.** A single scan of the top-level JSON object records the span of `model`, of `messages`
  or `input`, and of the first user message. The model swap is done by splicing those spans, and the session hash
  reuses them. Presize the read buffer from `Content-Length`.
- **Release bodies early.** Clear `info.body` once `modifyResponse` returns, and give the upstream a body reader that
  drops its bytes at EOF.
- **SSE scrubber.** Return every complete buffered line in one read without copying, and use an 8 KB buffer, not
  64 KB. Before running the regex on a header value, check case-insensitively whether it contains an engine name.
- **Leak fix.** Strip the front door's `extra_fields` from non-streamed JSON bodies, and drop `X-Cpa-*` response
  headers in `cleanResponseHeaders`. Add an e2e assertion for both.

### PR 3: Upstream latency and quality (effort M)

**Prompt-cache affinity.**
- The session key comes from a client-supplied id, checked in this order: `X-Session-ID`, `X-Opencode-Session`, then
  the body's `prompt_cache_key`. Without one, remora hashes the conversation up to and including the first *user*
  message, leaving out system and developer messages, so two conversations sharing a system prompt get different
  keys.
- remora sends it as `X-Bf-Eh-X-Session-Affinity`, next to the OpenCode header.
- Turn on the OAuth engine's `routing.session-affinity: true`, with `session-affinity-rebind-on: quota`. A rebind
  costs one turn's cache hit, not quality: the model and the conversation stay the same.
- Track per-account in-flight counts in the stats. If affinity concentrates load on one account, the
  `RATE_LIMIT_EXCEEDED` rate shows it.

**Thinking level as a visible alias choice.**
- An alias member may carry a thinking suffix (`(minimal)`, `(low)`, `(medium)`, `(high)`, or a budget number). The
  alias API and the console accept it and show it.
- No default changes for existing aliases: a client that sends `reasoning_effort` still controls it.
- A suffix on a model that has no thinking passes through unchanged. The engine ignores it, and remora does not keep
  a capability table for it.
- Document that a fast tier is a named alias such as `flash-fast`, never a silent default.

**Front-door patches** (each gets a row in `docs/patches.md`):
- No fallback on caller errors 400, 413 and 422.
- Drop the synthesised `reasoning_details` entry, the one without a signature. `reasoning` and `reasoning_content`
  stay, because clients read both.

**Load harness in the repo** (lands in PR 1).
- `tools/loadgen` holds the Go load generator from the audit.
- `make bench` brings up remora, the front door and the OAuth engine against the fake upstream. It runs 1, 10, 100
  and 500 concurrent, streamed and not, and prints overhead p50/p99, time to first token, requests per second, CPU
  per 100 streams and RSS for each process.
- It is not a CI gate. It is the regression guard run before a release.

**Pool health.** An alert, shown in the console and on `/healthz` detail, when accounts drop out of the pool after a
refresh failure. The pool shrinks silently today.

## Acceptance

- `make lint test` and `make image fakes-image e2e` pass for each PR.
- `make bench` on a 4 vCPU / 8 GB Linux host, compared with 0.2.0 on the same host:
  - at least 2× requests per second at 100 concurrent;
  - added p99 time to first token under 100 ms at 500 streams;
  - zero errors at 500 streams;
  - remora's RSS flat over a 30-minute soak.
- The owner runs this on the live subscription pool after PR 3 is merged; it is never scripted in this repo. Repeat the earlier comparison with 200 concurrent requests and with
  long multi-turn conversations. Report time to first token per account and the prompt-cache hit rate
  (`cachedContentTokenCount` in usage).
- The front door's log database stops growing after 7 days: from day 7 to day 14 it grows under 5% at steady
  traffic.

## Decided by the builder (my calls)

- Retention of 7 days, with content logging off by default and a setting to turn it on.
- A fallback chain of 3.
- A 30 s cooling default for subscription members.
- The `GOMEMLIMIT` shares.
- Hedging and bypassing the front door are deferred, not rejected. Each comes back only with data.
- The DeepSeek "reasoning eats max_tokens" result (1/5 against 4/5) is not significant at n=5. The next step is a
  diff of the upstream request bodies plus a rerun at n≥30, before any change.

## Left to the implementer

- Exact names of the new remora settings and of the pprof variable.
- How the snapshot is wired into the store's commit path.
- How `tools/loadgen` is laid out.
