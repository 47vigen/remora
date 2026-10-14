# remora 0.1 implementation plans

Spec: [`docs/specs/2026-10-14-remora-0.1-design.md`](../specs/2026-10-14-remora-0.1-design.md). Research:
[`docs/research/`](../research/README.md). Every plan implements part of that spec; read the spec first.

Plans run in order. Each one ends with software that builds, runs and passes its own tests.

| # | plan | delivers | depends on |
|---|---|---|---|
| 01 | [foundation](2026-10-14-01-foundation.md) | repo skeleton and open-source files, CI, store, supervisor, the image (engines built from source), health and version, e2e harness with fakes | — |
| 02 | [inference core](2026-10-14-02-inference-core.md) | client keys, `/v1` proxy, API-key providers, aliases and their compilation, `/v1/models`, reconciliation | 01 |
| 03 | [subscription accounts](2026-10-14-03-subscription-accounts.md) | the OAuth engine wired behind the front door, account login and status, dropped-params header, cooling of dead members | 02 |
| 04 | [speech-to-text](2026-10-14-04-speech-to-text.md) | the Soniox adapter, the `speaker` patch to the front door | 02 |
| 05 | [management API](2026-10-14-05-management-api.md) | the complete OpenAPI contract and handlers, logs and stats, export, apply, `import omniroute` | 02, 03, 04 |
| 06 | [web UI](2026-10-14-06-web-ui.md) | the HeroUI console, embedded in the binary | 05 |
| 07 | [release and parity](2026-10-14-07-release-and-parity.md) | release automation, engine bump workflow, `docs/parity.md` green, README quick start | 01–06 |

## Shared contracts

Every plan uses these names. A plan that needs something not listed here defines it in its own Interfaces block
and later plans consume it from there.

### Module and layout

- Go module `github.com/47vigen/remora`, Go 1.27 (the front door's `transports/go.mod` requires 1.27.0). Engines are built in their own module context from
  `engines/bifrost` and `engines/cliproxyapi` (git submodules), never imported.
- Packages: `cmd/remora`, `internal/{store,supervise,engine/bifrost,engine/cliproxyapi,auth,proxy,alias,cooling,soniox,reconcile,api,importer/omniroute,config,version}`.
- UI in `web/` (Bun, Vite). Management contract in `api/openapi.yaml`.

### Runtime

| name | default | meaning |
|---|---|---|
| `REMORA_LISTEN` | `:8080` | the only exposed listener |
| `REMORA_DATA` | `/data` | data volume |
| `REMORA_ADMIN_PASSWORD` | generated at first boot, printed once | admin password |
| `REMORA_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `REMORA_ENGINE_DIR` | `/usr/local/lib/remora/engines` | engine binaries and `versions.json` |
| `REMORA_MASTER_KEY` | read from `$REMORA_DATA/master.key`, generated if absent | encrypts secrets at rest |

`REMORA_ADMIN_PASSWORD` applies only while the store has no admin; afterwards the stored password wins.

Internal loopback ports, overridable with neutral names so no engine name reaches an operator:
`REMORA_PORT_ROUTER` = front door (Bifrost) `127.0.0.1:18080`, `REMORA_PORT_ACCOUNTS` = OAuth engine (CLIProxyAPI)
`127.0.0.1:18317`, `REMORA_PORT_SPEECH` = Soniox adapter `127.0.0.1:18090`.

Data layout: `$REMORA_DATA/remora.db`, `$REMORA_DATA/bifrost/`, `$REMORA_DATA/cliproxyapi/`.

### Core types (Go)

```go
// internal/store
type Store struct{ /* *sql.DB */ }
func Open(ctx context.Context, path string) (*Store, error)   // applies embedded migrations

// internal/alias
type Kind string // "chat" | "embeddings" | "transcription" | "decisions"
type Member struct{ Ref string } // an alias name, or "<provider>/<model>"
type Alias struct {
    Name          string
    Kind          Kind
    Members       []Member
    Capabilities  []string // advertised supported_parameters, e.g. "structured_outputs", "tools"
    ContextWindow int      // 0 = unset
}
type Target struct{ Provider, Model string }
func Compile(all map[string]Alias, name string, maxDepth int) ([]Target, error) // ErrCycle, ErrDepth, ErrUnknownMember

// internal/proxy
func ExtractModel(r *http.Request) (model string, body io.ReadCloser, err error) // JSON body or multipart "model"
func Allowed(patterns []string, model string) bool // an alias name or "<provider>/<model>"; "*" matches any name,
// a trailing "/*" matches any depth below it, otherwise path.Match rules

// internal/supervise
type Engine struct {
    Name      string // "bifrost" | "cliproxyapi"
    Bin       string
    Args, Env []string
    HealthURL string
}
type Supervisor struct{ /* ... */ }
func (s *Supervisor) Start(ctx context.Context, engines ...Engine) error // returns once all are healthy
func (s *Supervisor) Stop(ctx context.Context) error
```

Engine clients (`internal/engine/bifrost.Client`, `internal/engine/cliproxyapi.Client`) are defined in plan 02 and
plan 03 respectively; their method sets are listed in those plans' Interfaces blocks. The front door's log access
(`bifrost.Tail`, `SearchLogs`, `GetLog`, `Log`, `LogFilter`) is defined in plan 03 (cooling needs it first) and
extended, never redefined, by plan 05.

Cross-plan rulings (they override anything a plan says to the contrary):

- An engine on a fork for a pending patch is never bumped or rebased automatically; when the upstream fix ships in
  a release, the bump workflow opens a pull request that points the engine back at upstream (plan 07 owns this).
- Users see subscription providers by product name: `antigravity`, `codex`, `grok` (the engine's `xai`), and the
  others plan 03 lists. The internal front-door provider for the OAuth engine is `remora-oauth`; plan 02's
  reconcile treats it and its `remora-oauth-*` routing rules as known and never deletes them.
- The management API includes what the UI needs beyond the spec's table: compiling an unsaved alias, cancelling a
  login, stats grouped by key, alias or provider, the live-log WebSocket at `/api/logs/live`, and an import dry run
  that returns a diff (plan 05 owns them; plan 06 consumes them).
- **One provider model.** A provider has `name`, `kind`, optional `type`, optional `base_url`, and keys. `kind` is
  one of `builtin` (a provider the front door ships; `type` names it: `openrouter`, `openai`, `anthropic`,
  `gemini`, ...), `openai-compatible`, `typesafe-compatible` (decision endpoints), `soniox`. Subscription accounts
  are a separate resource (`accounts`), not a provider kind. The UI groups providers for display only: API key =
  `builtin` + `openai-compatible`, Decisions = `typesafe-compatible`, Speech = `soniox`, Subscription = accounts.
  Plans 02, 05 and 06 use exactly these wire values.
- **One `remora.yaml` format.** Plan 02 defines it (the file `remora apply` reads); plan 05 extends the same
  format with export, diff and `--yes` and publishes its JSON Schema. There is no second format.
- **Engine tests in CI.** Tests tagged `engine` (plan 03) run in a CI job that builds the OAuth engine from its
  submodule with `go build` (cgo) on the runner, the same way the Dockerfile does.

### Headers remora adds to `/v1` responses

`x-remora-alias`, `x-remora-served-by`, `x-remora-fallback` (`true`/`false`), `x-remora-dropped-params`
(comma-separated, only when non-empty).

### Testing

- Unit tests next to the code, `go test -race ./...`.
- End-to-end tests in `test/e2e` behind the build tag `e2e`. They run the built remora image and a fakes image
  (`test/fakes`, one Go binary serving an OpenAI-compatible fake, a Soniox fake and a TypeSafe fake, each with a
  control API to script 429, 402, latency and bodies) with Docker Compose (`test/e2e/compose.yaml`), and call
  remora on `localhost`. No real provider and no real key, ever.
- Persian text with ZWNJ (U+200C) is part of every streaming fixture and compared byte for byte.

### Commits

Conventional Commits with scopes (`feat(proxy):`, `test(e2e):`, `ci:`), on a branch per plan, one pull request per
plan into `main`.
