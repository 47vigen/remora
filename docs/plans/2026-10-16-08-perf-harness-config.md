# Performance: Harness and Configuration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Land a repeatable load harness, take the 0.2.0 baseline with it, then apply every configuration-level and
one-line fix from the performance design, and measure the gain with the same harness.

**Architecture:** `tools/loadgen` is a standalone Go load generator. `make bench` runs the built remora image and the
fakes image under fixed CPU and memory limits and runs a scenario matrix against them. The fixes touch only remora:
the store pool, the `/v1` transport and server, the cooling tail, the cooler, the config each engine gets rendered,
and the OAuth engine's runtime settings patch. No engine source changes.

**Tech Stack:** Go 1.27, `net/http`, `database/sql` (modernc SQLite), Docker Compose, the fakes image (`test/fakes`).

**Spec:** `docs/specs/2026-10-16-performance-design.md`, PR 1. The evidence is in the same spec, under "Evidence".

## Global Constraints

- Never edit `engines/`. Every engine change is configuration that remora renders or patches.
- No owner host, domain, product name or key anywhere, including bench fixtures. Bench keys are obvious test values
  (`rk-bench-…`).
- Engine names never reach a user-facing string. New env variables use neutral names (`REMORA_*`).
- Every OAuth engine setting goes in **both** `internal/supervise/cliproxyapi.go` (`cliproxyConfig`, the file at
  boot) and `settingsPatch` in `internal/accounts/reconcile.go` (runtime). Their tests compare every key.
- The OAuth engine keeps HTTP/1.1 to the provider. Do not add any HTTP/2 setting.
- Tests before code. `go test -race ./...` and `golangci-lint run` stay green after every task.
- Conventional Commits with scopes. Each commit has a body that says what changed and why.
- Branch `perf-harness`, one PR to `main`.

## Review Focus

1. **A store opened by both `serve` and a concurrent `remora apply`/`export` under load.** Raising the idle pool
   must not make writes fail with `SQLITE_BUSY`. Test: Task 2, `TestStoreConcurrentReadsAndWrite`.
2. **Container with no memory limit (`memory.max` = `max`) or no cgroup v2 at all, as on macOS.** Children must
   start without a `GOMEMLIMIT`, and must not get `GOMEMLIMIT=0`. Test: Task 3, `TestMemoryLimitShares`.
3. **The live log viewer opened while cooling runs.** With two tails, the viewer must still get success rows, and
   cooling must still get error rows. Test: Task 4, `TestTailStatusFilter` plus e2e `TestCoolingStillCools`.
4. **A 429 from a subscription member that carries a `Retry-After`.** The hint must win over the 30 s subscription
   default. Test: Task 5, `TestSubscriptionDefaultCooldown`.
5. **Upgrading a node whose OAuth engine config already holds the old four keys.** The new keys must reach the
   running engine without a restart. Test: Task 6, `TestReconcileAddsNewSettings`.

## File map

```
tools/loadgen/main.go             load generator (Task 1)
tools/loadgen/main_test.go        stats and SSE parsing tests (Task 1)
tools/bench/compose.yaml          remora + fakes under fixed limits (Task 1)
tools/bench/remora.yaml           bench provider, alias, key (Task 1)
tools/bench/run.sh                scenario matrix, docker stats sampling (Task 1)
Makefile                          bench target (Task 1)
.gitignore                        bench-results/ (Task 1)
internal/store/store.go           pool sizing (Task 2)
internal/proxy/proxy.go           transport idle pool (Task 2)
cmd/remora/serve.go               IdleTimeout, pprof listener, two tails, memory limit (Tasks 2, 3, 4)
internal/config/config.go         REMORA_PPROF_ADDR, REMORA_LOG_CONTENT (Tasks 3, 6)
internal/supervise/memlimit.go    cgroup memory limit and shares (Task 3)
internal/supervise/bifrost.go     logs retention, writer, content logging, telemetry off (Task 6)
internal/engine/bifrost/tail.go   status filter (Task 4)
internal/cooling/cooling.go       subscription default cooldown (Task 5)
internal/cooling/source.go        mark subscription events (Task 5)
internal/reconcile/desired.go     round-robin fallback cap (Task 5)
internal/reconcile/oauth.go       30 min request timeout on remora-oauth (Task 6)
internal/supervise/cliproxyapi.go new OAuth engine keys (Task 6)
internal/accounts/reconcile.go    settingsPatch with the same keys (Task 6)
docs/plans/README.md              runtime table rows for the new variables (Task 6)
```

---

### Task 1: Load harness and the 0.2.0 baseline

**Files:**
- Create: `tools/loadgen/main.go`, `tools/loadgen/main_test.go`, `tools/bench/compose.yaml`, `tools/bench/remora.yaml`,
  `tools/bench/run.sh`
- Modify: `Makefile`, `.gitignore`

**Interfaces:**
- Produces: `go run ./tools/loadgen -url U -key K -model M -c N -d D [-stream] [-get] [-body-kb N]`, which prints one
  line: `c=N rps=… p50=… p90=… p99=… ttft_p50=… ttft_p99=… ok=… err=…`. `make bench` writes
  `bench-results/<git describe>.txt`.

The generator starts from the prototype the audit used (shared HTTP transport with 4096 idle connections, fixed
concurrency, a duration, total latency and TTFT taken at the first `data:` line). `-body-kb` pads the user message so
the request size can be varied.

- [ ] **Step 1: Write the failing tests** in `tools/loadgen/main_test.go`:
  - `TestPercentiles`: for durations 1..100 ms, `percentile(d, 0.50) == 50ms` and `percentile(d, 0.99) == 99ms`.
  - `TestFirstDataEvent`: an SSE body `": keepalive\n\ndata: {\"x\":1}\n\n"`, read through `countData(r, onFirst)`, counts 2
    data events and calls `onFirst` once, at the first `data:` line and not at the comment line.
  - `TestBodyPadding`: `requestBody("m", true, 64)` is valid JSON, `stream` is true, and it is at least 64 KiB.
- [ ] **Step 2: Run** `go test ./tools/loadgen/`. Expected: FAIL (undefined).
- [ ] **Step 3: Implement** `percentile(sorted []time.Duration, q float64) time.Duration`,
  `countData(r *bufio.Reader, onFirst func()) (int, error)`, `requestBody(model string, stream bool, kb int) []byte` and `main`.
- [ ] **Step 4: Run** `go test ./tools/loadgen/`. Expected: PASS.
- [ ] **Step 5: Write the bench stack.**
  - `tools/bench/compose.yaml`: services `fakes` (`${FAKES_IMAGE}`) and `remora` (`${REMORA_IMAGE}`), with remora
    under `cpus: 4` and `mem_limit: 8g`. Ports are published on 127.0.0.1 only. Env carries `BENCH_UPSTREAM_KEY`
    and `BENCH_KEY=rk-bench-0123456789abcdef`.
  - `tools/bench/remora.yaml`: provider `up` (`openai-compatible`, `base_url: http://fakes:9090/u/a`,
    `allow_private_network: true`), alias `fast` → `up/m1`, key `bench` with `allow: ["*"]`.
- [ ] **Step 6: Write `tools/bench/run.sh`** (bash, `set -euo pipefail`). It:
  - brings the stack up and waits for `/healthz`;
  - runs `remora apply -f /bench/remora.yaml --yes` through `docker compose exec`;
  - scripts the fake through its `/_control` API the way `test/e2e/fakesctl` does: non-stream answers at once, and
    streams of 40 chunks 25 ms apart;
  - runs this matrix: `/v1/models` at c=10; non-stream at c=1, 10, 100; stream at c=100, 500; non-stream with
    `-body-kb 256` at c=50;
  - samples `docker stats --no-stream` for remora every second during each run and records peak CPU% and peak RSS;
  - writes everything to `bench-results/$(git describe --tags --always --dirty).txt`, then takes the stack down.
- [ ] **Step 7: Add the target.** `bench: ; tools/bench/run.sh` (it runs against already-built images, like `e2e`).
  Add `bench-results/` to `.gitignore`.
- [ ] **Step 8: Take the baseline.** `make image fakes-image && make bench`. Expected: the file exists, and every
  scenario has `err=0`. Keep it as `bench-results/baseline-0.2.0.txt`. The PR description quotes it, and it is
  never committed.
- [ ] **Step 9: Commit**: `feat(tools): add the load generator and make bench`.

### Task 2: Store pool, upstream pool, idle timeout

**Files:**
- Modify: `internal/store/store.go:32-43`, `internal/proxy/proxy.go:58-67`, `cmd/remora/serve.go:313-320`
- Test: `internal/store/store_test.go`, `internal/proxy/proxy_test.go`, `cmd/remora/serve_test.go`

**Interfaces:**
- Produces: constants `store.maxConns = 16`, `proxy.upstreamIdleConns = 1024`, `idleTimeout = 120 * time.Second`
  (in `cmd/remora`).

- [ ] **Step 1: Write the failing tests.**
  - `TestStorePoolKeepsConnections` (store): after `Open`, `s.DB().Stats().MaxOpenConnections == 16`. After 16
    parallel `ClientKeyByValue` calls finish, `Stats().Idle == 16`.
  - `TestStoreConcurrentReadsAndWrite`: 32 goroutines reading keys while one `Write` transaction inserts an alias;
    no error, and the alias is read back.
  - `TestUpstreamTransportPool` (proxy): `p.transport.MaxIdleConnsPerHost == 1024` and `MaxIdleConns == 1024`.
  - `TestServerTimeouts` (cmd/remora): `newServer(...).IdleTimeout == 120*time.Second`, and `ReadHeaderTimeout` is
    unchanged.
- [ ] **Step 2: Run** `go test -race ./internal/store/ ./internal/proxy/ ./cmd/remora/`. Expected: FAIL on the
  four assertions.
- [ ] **Step 3: Implement.**
  - In `Open`: `db.SetMaxOpenConns(maxConns)`, `db.SetMaxIdleConns(maxConns)`, `db.SetConnMaxIdleTime(0)`.
  - Set both transport fields to `upstreamIdleConns`.
  - Add `IdleTimeout: idleTimeout` in `newServer`.
- [ ] **Step 4: Run** the same command. Expected: PASS.
- [ ] **Step 5: Commit**: `perf(store): keep a warm SQLite pool`, with a body that quotes the audit's 49% CPU
  finding. Then `perf(proxy): size the upstream pool for 1000 streams` and `fix(serve): close idle client
  connections after 120s`.

### Task 3: Memory limits for every process, opt-in pprof

**Files:**
- Create: `internal/supervise/memlimit.go`, `internal/supervise/memlimit_test.go`
- Modify: `internal/supervise/bifrost.go` and `cliproxyapi.go` (append to `Env`), `internal/config/config.go`,
  `cmd/remora/serve.go`

**Interfaces:**
- Produces:
  - `supervise.MemoryLimit(read func(string) ([]byte, error)) int64`: the cgroup v2 `memory.max` in bytes, or 0
    when it is missing, `max`, or unreadable. It reads `/sys/fs/cgroup/memory.max` through `read`.
  - `supervise.MemoryShares = struct{ Router, Accounts, Self float64 }{0.50, 0.15, 0.10}`.
  - `supervise.GoMemLimitEnv(limit int64, share float64) []string`: `nil` when `limit == 0`, otherwise
    `["GOMEMLIMIT=<limit*share>"]`.
  - `config.Config.PprofAddr string`, from `REMORA_PPROF_ADDR`, empty by default.
  - `BifrostEngine` and `CLIProxyAPIEngine` take the limit through `config.Config.MemoryLimit int64`, which serve
    fills before rendering.

- [ ] **Step 1: Write the failing tests.**
  - `TestMemoryLimitShares`, table-driven:
    - `"4294967296\n"` gives 4294967296;
    - `"max\n"` gives 0;
    - a read error gives 0;
    - `GoMemLimitEnv(4294967296, 0.5)` is `["GOMEMLIMIT=2147483648"]`;
    - `GoMemLimitEnv(0, 0.5)` is nil.
  - Extend `TestBifrostEngine` and `TestCLIProxyAPIEngine` (`engines_test.go`): with `cfg.MemoryLimit = 4<<30`, the
    Env contains `GOMEMLIMIT=2147483648` and `GOMEMLIMIT=644245094` respectively. With 0, no `GOMEMLIMIT` entry.
  - `TestConfigPprofAddr`: `REMORA_PPROF_ADDR=127.0.0.1:6060` loads into `PprofAddr`, and a non-loopback host is
    rejected with an error.
- [ ] **Step 2: Run** `go test -race ./internal/supervise/ ./internal/config/`. Expected: FAIL.
- [ ] **Step 3: Implement** the three functions and the config field.
  - In `serve`: read `MemoryLimit` once. If it is non-zero and the operator set no `GOMEMLIMIT`, call
    `debug.SetMemoryLimit(limit * MemoryShares.Self)` for remora itself.
  - When `PprofAddr` is set, serve `net/http/pprof` on its own `http.Server` bound to that address, with its own mux
    (never on the public mux). It is shut down with the main listener.
- [ ] **Step 4: Run** the same command. Expected: PASS.
- [ ] **Step 5: Commit**: `feat(supervise): give each process a share of the container memory`, then
  `feat(serve): opt-in loopback pprof`.

### Task 4: Cooling reads only error rows

**Files:**
- Modify: `internal/engine/bifrost/tail.go`, `cmd/remora/serve.go:157-159,286-295`
- Test: `internal/engine/bifrost/tail_test.go`

**Interfaces:**
- Produces: `bifrost.NewTail(src LogSource, interval time.Duration, status []string, log *slog.Logger) *Tail`. `status`
  goes into every `SearchLogs` filter; nil means every row, which is today's behaviour.
- serve builds two tails:
  - `coolTail`, with `[]string{"error"}`, feeds cooling;
  - `liveTail`, with nil, is `api.Deps.Tail` for the live log viewer.
- Both run under the same `loops` group. `liveTail` polls only while a viewer is subscribed (the existing
  `subscribers() > 0` check).

- [ ] **Step 1: Write the failing tests.**
  - `TestTailStatusFilter`: a fake `LogSource` records the filters it receives. A tail built with `[]string{"error"}`
    and one subscriber polls once, and every recorded filter has `Status == []string{"error"}`. A tail built with
    nil sends `Status == nil`.
  - Update every existing `NewTail` call in tests to pass nil.
- [ ] **Step 2: Run** `go test -race ./internal/engine/bifrost/`. Expected: FAIL (signature).
- [ ] **Step 3: Implement** the new parameter, and wire the two tails in serve.
- [ ] **Step 4: Run** `go test -race ./...`. Expected: PASS.
- [ ] **Step 5: e2e check.** `test/e2e/cooling_test.go` and the live-log assertions in `management_test.go` still
  pass, and need no change. They run in Task 7.
- [ ] **Step 6: Commit**: `perf(cooling): follow only error rows of the request log`, with a body that explains the
  10-minute window was being re-read every second, bodies included.

### Task 5: A short cooldown for subscription members, and a capped fallback chain

**Files:**
- Modify: `internal/cooling/cooling.go:20-35,180-186`, `internal/cooling/source.go:25-41`,
  `internal/reconcile/desired.go:288-295`, `internal/config/config.go`, `cmd/remora/serve.go` (pass the new value)
- Test: `internal/cooling/cooling_test.go`, `internal/cooling/source_test.go`, `internal/reconcile/desired_test.go`

**Interfaces:**
- Produces:
  - `cooling.Event.Subscription bool`;
  - `cooling.Config.SubscriptionDefault time.Duration`;
  - `cooling.DefaultSubscriptionCooldown = 30 * time.Second`;
  - env `REMORA_COOLING_SUBSCRIPTION_DEFAULT`;
  - `reconcile.maxFallbacks = 3`.

- [ ] **Step 1: Write the failing tests.**
  - `TestSubscriptionDefaultCooldown`:
    - a subscription event with no retry hint cools for 30 s;
    - the same event with `RetryAfter: 2*time.Minute` cools for 2 min;
    - a non-subscription event with no hint still cools for `Default` (5 min).
  - `TestEventFromMarksSubscription`: a log row whose `Provider` is `bifrost.OAuthProvider` gives
    `Event.Subscription == true`. A row from an API-key provider gives false.
  - `TestRoundRobinFallbacksCapped`: a round-robin alias with 5 members compiles to a rule where
    `len(rule.Fallbacks) == 3` and `rule.Spread` still holds the other 4. An ordered alias with 5 members keeps all
    4 fallbacks. This is unchanged: ordered chains are the user's explicit order.
- [ ] **Step 2: Run** `go test -race ./internal/cooling/ ./internal/reconcile/`. Expected: FAIL.
- [ ] **Step 3: Implement.**
  - In `coolLocked`, the hint wins. Otherwise the cooldown is `SubscriptionDefault` for subscription events, and
    `Default` for every other event.
  - In `desired.go`, the round-robin branch becomes `rule.Spread, rule.Fallbacks = routes[1:],
    routes[:min(len(routes), maxFallbacks)]`.
  - Add a one-line comment saying why: the front door can't tell which spread member it picked, so one of the
    three may repeat it.
- [ ] **Step 4: Run** the same command. Expected: PASS.
- [ ] **Step 5: Commit**: `fix(cooling): cool a subscription member for 30s without a hint`, then
  `perf(alias): cap a round-robin fallback chain at three`.

### Task 6: Engine configuration

**Files:**
- Modify: `internal/supervise/bifrost.go`, `internal/supervise/cliproxyapi.go`, `internal/accounts/reconcile.go:50-66`,
  `internal/reconcile/oauth.go:27-40`, `internal/config/config.go`, `docs/plans/README.md` (runtime table)
- Test: `internal/supervise/engines_test.go`, `internal/supervise/cliproxyapi_test.go`,
  `internal/accounts/reconcile_test.go`, `internal/reconcile/oauth_test.go`

**Interfaces:**
- Produces: `config.Config.LogContent bool`, from `REMORA_LOG_CONTENT`, default false.

Before writing code, confirm each key against the pinned engine source (`engines/versions.env`). Record the file and
line of each in the commit body.
- **OAuth engine** (`C/internal/config/config_v8.go` and `C/config.example.yaml`):
  - `routing.retry.request-retry`
  - `routing.retry.max-retry-interval`
  - `routing.cooldown.transient-error-cooldown-seconds`
  - `requests.streaming.bootstrap-retries`
  - `requests.streaming.keepalive-seconds` (lens 5 calls it `streaming.keepalive-seconds`; use the path the source
    shows)
  - `oauth.providers.antigravity.connection-pool.{enabled,idle-conn-timeout,max-idle-conns-per-host}`
  - Confirm also that `request-retry` retries only before the first byte reaches the client
    (`C/sdk/api/handlers/handlers_stream.go`). If it can retry after that, leave `request-retry` and
    `max-retry-interval` out, and say so in the PR.
- **Front door** (`B/framework/logstore`, `B/transports/bifrost-http/lib/config.go`, `B/plugins/logging`):
  - `logs_store.retention_days`
  - `logs_store.writer.batch_interval`
  - `client.log_retention_days`
  - `client.disable_content_logging`
  - `plugins[{name: telemetry, enabled: false}]`

- [ ] **Step 1: Write the failing tests.**
  - `TestBifrostEngine`, extended. The rendered `config.json` has:
    - `logs_store.retention_days == 7`;
    - `logs_store.writer.batch_interval == "1s"`;
    - `client.log_retention_days == 7`;
    - `client.disable_content_logging == true` when `LogContent` is false, and `false` when it is true;
    - `plugins == [{"name":"telemetry","enabled":false}]`.
  - `TestCLIProxyAPIEngineRendersSettings`, extended. The YAML has:
    - `routing.retry.request-retry: 1`, `routing.retry.max-retry-interval: 10`;
    - `routing.cooldown.transient-error-cooldown-seconds: 5`, with `save-cooldown-status: true` kept;
    - `requests.streaming.bootstrap-retries: 1`, `requests.streaming.keepalive-seconds: 15`;
    - `oauth.providers.antigravity.connection-pool: {enabled: true, idle-conn-timeout: 90s,
      max-idle-conns-per-host: 8}`.
  - `TestReconcilePatchesSettings`, extended: the patch sent to the fake engine has exactly the same keys and values
    as the file. Use one shared table in the test, so the two can't drift.
  - `TestReconcileAddsNewSettings`: the fake engine starts with only the old four keys, and one `Reconcile` pass
    patches in the new ones.
  - `TestOAuthProviderTimeout`: the internal `remora-oauth` provider has `RequestTimeout == 30*time.Minute`. Every
    other provider still renders 600 s.
- [ ] **Step 2: Run** `go test -race ./internal/supervise/ ./internal/accounts/ ./internal/reconcile/`. Expected: FAIL.
- [ ] **Step 3: Implement.**
  - Extend the config structs in `bifrost.go` (`LogsStore` gains `RetentionDays` and `Writer`, plus new `Client` and
    `Plugins` fields) and in `cliproxyapi.go` (`Routing.Retry`, `Cooldown.TransientErrorCooldownSeconds`, a new
    `Requests` section, `OAuth.Providers`).
  - Extend `settingsPatch` with the same values.
  - Set `RequestTimeout: 30 * time.Minute` in `oauth.go`.
  - Add `REMORA_LOG_CONTENT` and `REMORA_COOLING_SUBSCRIPTION_DEFAULT` to the runtime table in
    `docs/plans/README.md`, and to the README's configuration section if it lists variables.
- [ ] **Step 4: Run** the same command. Expected: PASS.
- [ ] **Step 5: Engine contract check.** If the `engine`-tagged contract tests cover config loading, run
  `go test -tags engine ./internal/engine/cliproxyapi/`. Expected: PASS, and the engine starts with the new file.
- [ ] **Step 6: Commit**:
  - `perf(supervise): bound the request log and stop the unused telemetry plugin`;
  - `perf(accounts): reuse provider connections and absorb short capacity spikes`;
  - `fix(reconcile): let subscription requests think for 30 minutes`.

### Task 7: Gates and measurement

- [ ] **Step 1:** `make lint test`. Expected: 0 issues, all packages `ok`.
- [ ] **Step 2:** `make image fakes-image e2e`. Expected: `ok github.com/47vigen/remora/test/e2e`.
- [ ] **Step 3:** `make bench`. Compare with `bench-results/baseline-0.2.0.txt`. Expected direction:
  - non-stream c=100 rps is at least 2× the baseline;
  - stream c=500 ttft_p99 is under 100 ms;
  - `err=0` everywhere;
  - peak remora RSS is no higher than the baseline.
  - If any expectation fails, profile with `REMORA_PPROF_ADDR` before changing anything.
- [ ] **Step 4:** Check that the front-door log rows no longer carry content. Do one request on the bench stack, then
  run `sqlite3 /data/bifrost/logs.db "select length(input_history) from logs order by timestamp desc limit 1"`
  through `docker compose exec`. Expected: 0 or NULL.
- [ ] **Step 5:** Push the branch `perf-harness` and open the PR. Its body holds the baseline and after tables from
  the two bench files, and the list of engine keys with their source lines.
