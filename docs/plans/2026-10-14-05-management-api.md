# Management API Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship remora's complete `/api/*` management surface — the OpenAPI contract, generated Go server, admin authentication, handlers for every resource, logs and stats over the front door's log store, the live log stream — and the `remora export`, `remora apply` and `remora import omniroute` commands.

**Architecture:** `api/openapi.yaml` (OpenAPI 3.1) is the source. `oapi-codegen` generates a strict `net/http` server, its models and a Go client into `internal/api/gen`. `internal/api.Server` implements the strict interface over small port interfaces (one per resource) that plans 02–04's services satisfy through thin adapters, so handlers are tested with in-memory fakes. Logs and stats are read from Bifrost's `/api/logs*` through plan 03's log client, extended here, and translated to remora names; the live stream is a remora WebSocket fed by plan 03's `bifrost.Tail` poller, because Bifrost's `/ws` does not carry log rows (see the note in Task 11). The CLI commands talk to a running remora through the generated client; `remora.yaml` is diffed and applied by the server.

**Tech Stack:** Go 1.27, `oapi-codegen` v2.8+ (`go tool`), `github.com/oapi-codegen/runtime` v1.6+, `github.com/getkin/kin-openapi` (tests), `github.com/coder/websocket`, `golang.org/x/crypto/{bcrypt,scrypt}`, `go.yaml.in/yaml/v3`, `github.com/santhosh-tekuri/jsonschema/v6`, the SQLite driver plan 01 chose (`modernc.org/sqlite`, driver name `sqlite`).

**Spec:** `docs/specs/2026-10-14-remora-0.1-design.md` (sections "Management API", "Source of truth and GitOps", "Authentication", "Keys", "Aliases"). Shared contracts: `docs/plans/README.md`. Research: `docs/research/2026-10-14-engine-management-apis.md`, `docs/research/2026-10-14-migrating-from-omniroute.md`.

## Global Constraints

- Go module `github.com/47vigen/remora`, Go 1.27; packages only from the shared list (`internal/{api,auth,config,importer/omniroute,engine/bifrost,engine/cliproxyapi,store,...}`), plus generated `internal/api/gen`.
- Contract first: `api/openapi.yaml` is the source; Go server interfaces generated with `oapi-codegen`; the UI client is plan 06's (`@hey-api/openapi-ts`).
- One admin. `REMORA_ADMIN_PASSWORD` applies only while no admin exists in the store, else a password is generated at
  first boot and printed once (plan 01's `auth.EnsureAdmin`); afterwards the stored password wins and is changed in
  Settings, with a 12-character minimum enforced by the server. UI: secure, HTTP-only session cookie. Scripts: API token as bearer on `/api/*`. Client keys work only on `/v1/*`.
- No engine name (`bifrost`, `cliproxyapi`, `CLIProxyAPI`, `sk-bf-`) ever reaches a user: not in a provider name, an error message, a log row or a stat.
- remora drives engines only through their runtime management APIs; if an engine call fails, the store change is rolled back and the error returned (owned by plans 02/03's services; handlers surface it as `502 engine_error`).
- `remora export` writes `remora.yaml` without secrets; secrets appear as `env:NAME`. `remora apply -f` shows a diff first and changes nothing without `--yes`. Export → apply is a round trip with no diff.
- `remora import omniroute` reads OmniRoute's `storage.sqlite` read-only and keeps every imported client key's exact value.
- Logs list filters: key, alias, served model, provider, status, time range, text. Stats: volume, error rate, latency p50/p95, tokens, cost, by key/alias/provider over a time range.
- Every list is paged, filterable and searchable.
- Tests: `go test -race ./...`; e2e under `test/e2e` behind build tag `e2e`, fakes only, no real key, never real OmniRoute data.
- Conventional Commits with scopes; branch per plan; no owner hosts, domains, product names or secrets in the repo.
- Engine source pinned for the facts below: Bifrost `72457fe`, CLIProxyAPI `0d18d3b`, OmniRoute `9ecddd15`.

## Review Focus

1. **An OmniRoute `STORAGE_ENCRYPTION_KEY` that contains `=`** (base64 padding) read from the data directory's env file: OmniRoute's own loader does `split("=", 2)[1]` (`src/lib/db/encryption.ts`, `ensureSecretLoaded`), which drops everything after the second `=`. A user expects the import to decrypt either way → importer tries the full value, then the truncated one (test in Task 15).
2. **`remora apply` with a secret's env var unset** (the normal case after `remora export` on another machine): the user expects "keep the stored secret, no diff" for an existing provider, and a clear error naming the variable for a new one (test in Task 13).
3. **Engine names inside log and error text** (Bifrost error bodies, the internal OAuth-engine provider name in a log row): the user expects remora names only (test in Task 8).
4. **Stats over a range with zero requests**: the user expects zeros and `null` percentiles, not a 500 or `NaN` (test in Task 10).
5. **A slow live-log client** (a browser tab in the background): the user expects other viewers and the poller to keep going; the slow socket is closed, not buffered without bound (test in Task 11).

---

## Names consumed from plans 01–04

Plans 01–04 exist; every name below is theirs. Handlers still talk to small port interfaces defined **in this plan**
(`internal/api/ports.go`, Task 3) so they are tested with in-memory fakes; `internal/api/adapters.go` maps the ports
onto these names one to one.

| from | name |
|---|---|
| 01 | `store.Store` with `DB()`, `Setting(ctx, key) (string, bool, error)`, `SetSetting(ctx, key, value)`; the `settings` table; migrations `internal/store/migrations/NNNN_*.sql` in one global sequence, this plan's file is `0005_auth.sql`; `auth.EnsureAdmin`, `auth.CheckAdminPassword`, `auth.SettingAdminPasswordHash` (`admin_password_hash`, bcrypt) — `serve` already calls `EnsureAdmin`; `cmd/remora` `run` dispatcher; `supervise.Supervisor.Status()` and `GET /api/system/version` (`VersionResponse {remora, engines: [{name, version}]}`); e2e helpers `remoraURL`, `fakesURL`, `execIn`, `resetFakes`, `waitHealthy`, compose sets `REMORA_ADMIN_PASSWORD: e2e-admin-password`, fakes at `http://fakes:9090/u/<upstream>`. |
| 02 | `store.ErrNotFound`, `store.ErrDuplicate`; `store.Write`/`Tx` repositories for providers, provider keys, client keys (`PutClientKey(ctx, k, value)` keeps a caller-chosen value) and aliases; `store.AliasRuleIDs`, `store.ClientKeyVKIDs` (engine ids for log attribution); `reconcile.Reconciler.Change` (store + engine with rollback); provider kinds `builtin` (with `type`), `openai-compatible`, `typesafe-compatible`, `soniox`; the `remora.yaml` format `config.File` and `config.Parse` (`internal/config/file.go`); `alias.Compile`, `alias.ValidName`; `bifrost.Client.do(ctx, method, path string, in, out any) error` (a query string is encoded into `path`); `bifrost.OAuthProvider`. |
| 03 | `accounts.Service` (`StartLogin`, `CompleteLogin`, `LoginStatus`, `CancelLogin`, `List`, `SetEnabled`, `Delete`, `ResetCooldown`, `Import` — `Import` uploads with `cliproxyapi.Client.UploadCredential`); `cooling.Cooler.Snapshot()`; `cliproxyapi.Providers()` (user names `antigravity`, `codex`, `grok`, …) and `cliproxyapi.UserRef`; `bifrost.LogFilter`, `Log`, `LogPage`, `Client.SearchLogs`, `Client.GetLog`, `Log.Failure`, `bifrost.Tail` (plan 03 Task 8; the one `Tail` that `serve` creates in plan 03 Task 10). |
| 04 | the `soniox` provider kind, managed through the same provider repositories. |

## File map

```
api/openapi.yaml                      contract (Task 1)
api/oapi-codegen.yaml                 codegen config (Task 1)
api/remora.schema.json                remora.yaml JSON Schema (Task 13)
internal/api/gen/generate.go          go:generate directive (Task 1)
internal/api/gen/api.gen.go           generated, committed (Task 1)
internal/api/contract_test.go         OpenAPI convention test (Task 1)
internal/auth/{password,session,token,limiter,middleware}.go          (Task 2)
internal/store/auth.go + migrations/0005_auth.sql                      (Task 2)
internal/api/{server,ports,adapters,errors,page,system,authn}.go       (Task 3)
internal/api/{providers,accounts,aliases,keys}.go                      (Tasks 4–7)
internal/engine/bifrost/logs.go (extends plan 03's)                    (Task 8)
internal/api/{translate,logs,stats,livelogs,settings,config}.go       (Tasks 8–13)
internal/config/{document,export,plan,apply,secrets}.go               (Task 13)
cmd/remora/{client,export,apply,import}.go                            (Tasks 14, 16)
internal/importer/omniroute/{decrypt,read,convert,credentials}.go     (Tasks 15–16)
test/e2e/management_test.go                                           (Task 17)
.github/workflows/ci.yml (modify: drift step)                         (Task 1)
```

---

### Task 1: OpenAPI contract, code generation, drift check

**Files:**
- Create: `api/openapi.yaml`, `api/oapi-codegen.yaml`, `internal/api/gen/generate.go`, `internal/api/gen/api.gen.go` (generated), `internal/api/contract_test.go`
- Modify: `go.mod` (tool directive), `.github/workflows/ci.yml` (drift step)

**Interfaces:**
- Consumes: nothing.
- Produces: package `gen` with `StrictServerInterface`, `NewStrictHandler`, `HandlerFromMuxWithBaseURL`, request/response types per operation, and `ClientWithResponses` (used by the CLI, Task 14). Operation ids below are the method names every later task implements.

Version note: OpenAPI **3.1**. oapi-codegen v2.8.0 added initial 3.1 support (nullability as `type: [T, "null"]`, requires Go 1.25 and runtime v1.6.0+); the UI generator handles 3.1 too. If generation of a 3.1 construct fails, express it the 3.0-compatible way inside the 3.1 document (e.g. avoid `type` arrays except for nullability) rather than downgrading the whole file.

**Conventions (defined once in `components`):**
- `parameters`: `Limit` (`limit`, int, 1–200, default 50), `Offset` (`offset`, int ≥ 0, default 0), `Query` (`q`, string, free-text search over name-like fields), `From`/`To` (RFC 3339; default last 24 h).
- Every list response is `{items: [...], total: integer}`, both required.
- `Error`: `{error: {message: string, type: string, code: string}}` (same shape as `/v1`). Every operation has `default: {$ref: '#/components/responses/Error'}`. Codes: `invalid_request` 400, `unauthorized` 401, `forbidden` 403, `not_found` 404, `conflict` 409, `too_many_attempts` 429, `engine_error` 502.
- `securitySchemes`: `session` (apiKey in cookie `remora_session`), `token` (http bearer). Global `security: [{session: []}, {token: []}]`; `getHealth` and `login` override with `security: []`.

**Paths (operationId — method path — notes):**

| tag | operation |
|---|---|
| system | `getHealth` GET `/api/health` → `{status: ok\|degraded, engines: {name: ok\|error}}`; `getVersion` GET `/api/system/version` → plan 01's `VersionResponse` `{remora, engines: [{name, version}]}` |
| auth | `login` POST `/api/auth/login` `{password}` → 204 + cookie; `logout` POST `/api/auth/logout` → 204; `getSession` GET `/api/auth/session` → `{authenticated: true, via: session\|token}` |
| providers | `listProviders` GET `/api/providers` (Limit, Offset, Query, `kind`, `type`); `createProvider` POST; `getProvider` GET `/api/providers/{name}`; `updateProvider` PUT; `deleteProvider` DELETE; `testProvider` POST `/api/providers/{name}/test` → `{ok, latency_ms, message}` |
| accounts | `listAccounts` GET `/api/accounts` (Limit, Offset, Query, `provider`); `startAccountLogin` POST `/api/accounts/logins` `{provider}` → `{state, url, user_code?}`; `getAccountLogin` GET `/api/accounts/logins/{state}` → `{status: wait\|ok\|error, message?}`; `completeAccountLogin` POST `/api/accounts/logins/{state}/complete` `{redirect_url}` → 202; `cancelAccountLogin` DELETE `/api/accounts/logins/{state}` → 204; `updateAccount` PATCH `/api/accounts/{id}` `{enabled}`; `deleteAccount` DELETE `/api/accounts/{id}`; `resetAccountCooldown` POST `/api/accounts/{id}/reset-cooldown`; `importAccount` POST `/api/accounts/import` `{provider, credential: object}` → Account |
| aliases | `listAliases` (Limit, Offset, Query, `kind`); `createAlias`; `getAlias` `/api/aliases/{name}`; `updateAlias` PUT; `deleteAlias`; `getAliasChain` GET `/api/aliases/{name}/chain` → `{targets: [{provider, model, cooling_until?}]}`; `compileAlias` POST `/api/aliases/compile` an unsaved `Alias` → `{targets: [{provider, model, cooling_until?}]}`, nothing stored (400 `invalid_request` on a cycle, depth or unknown member); `listCooling` GET `/api/cooling` → `{items: [{provider, model, until, reason, aliases[]}], total}` |
| keys | `listKeys` (Limit, Offset, Query); `createKey` POST `{name, allow[], value?}` → `KeyCreated` (Key + `value`, the only time it is returned); `getKey` `/api/keys/{id}`; `updateKey` PATCH `{name?, allow?, enabled?}`; `deleteKey`; `getKeyUsage` GET `/api/keys/{id}/usage` (From, To) → `Stats` |
| logs | `listLogs` GET `/api/logs` (Limit, Offset, From, To, `q`, `key`, `alias`, `provider`, `model` = served `<provider>/<model>`, `status` = `success\|error`); `getLog` `/api/logs/{id}` → `LogDetail`; `streamLogs` GET `/api/logs/live` (optional `key`, `alias`, `provider`, `status`) — the live-log WebSocket, authenticated by the session cookie (or an API token), documented with response `101` and `x-websocket: true`, excluded from codegen (see config) |
| stats | `getStats` GET `/api/stats` (From, To, `group_by` = `none\|key\|alias\|provider`, `key`, `alias`, `provider`) → `Stats`; with `group_by` set, `groups` holds one summary per key, alias or provider |
| settings | `changePassword` PUT `/api/settings/password` `{current, new}` → 204; `listApiTokens` GET `/api/settings/tokens`; `createApiToken` POST `{name}` → `ApiTokenCreated` (with `value`); `deleteApiToken` DELETE `/api/settings/tokens/{id}` |
| config | `exportConfig` GET `/api/config/export` → `application/yaml` string; `planConfig` POST `/api/config/plan` `ConfigRequest` → `{changes: [Change]}` — the import dry run: it returns the diff and changes nothing; `applyConfig` POST `/api/config/apply` `ConfigRequest` → `{changes: [Change]}` |

**Schemas (fields; `?` = optional):**
- `Provider {name, kind: builtin|openai-compatible|typesafe-compatible|soniox, type? (builtin only: the front door's provider, e.g. `openrouter`), base_url?, allow_private_network?, key_count (read-only), status (read-only: `ok` | `degraded` | `down` | `unknown` — `down` when the last test failed or every model of the provider is cooling, `degraded` when some are cooling, `unknown` before the first test), keys?: [{name, value}]}` — the README's one provider model, the same fields as `remora.yaml`; `value` is write-only, so responses list key names only. Subscription accounts are the `accounts` resource, never a provider kind.
- `Account {id, provider, email?, enabled, status, message?, cooldown_until?, quota: [{model?, remaining_fraction?, reset_at?}], models: string[]}`
- `Alias {name, kind: chat|embeddings|transcription|decisions, members: string[], capabilities: string[], context_window?}`
- `Key {id, name, allow: string[], enabled, prefix, created_at}`; `KeyCreated` = Key + `value`
- `LogEntry {id, parent_id?, attempt (int), time, key?, alias?, provider, model, status: success|error|processing|cancelled, latency_ms?, tokens: {prompt, completion, total}, cost_usd?, stream, error?}`
- `LogDetail` = LogEntry + `request?: object`, `response?: object`, `fallback_path: [LogEntry]` (root first, by attempt).
- `StatsSummary {requests, errors, error_rate, latency_p50_ms?, latency_p95_ms?, prompt_tokens, completion_tokens, total_tokens, cost_usd}`; `Stats {from, to, summary: StatsSummary, series: [{time, requests, errors}], groups: [{name, summary: StatsSummary}]}`
- `ApiToken {id, name, prefix, created_at, last_used_at?}`; `ApiTokenCreated` = ApiToken + `value`
- `ConfigRequest {document: string (remora.yaml), secrets: {NAME: value}?, prune: boolean (default true)}`; `Change {op: create|update|delete, kind: provider|alias|key, name, fields: string[]}`

**Codegen config (`api/oapi-codegen.yaml`):** `package: gen`, `output: internal/api/gen/api.gen.go`, `generate: {std-http-server: true, strict-server: true, models: true, client: true}`, `output-options: {exclude-operation-ids: [streamLogs]}`.

- [ ] **Step 1: Write the failing convention test** — `internal/api/contract_test.go`, loads `../../api/openapi.yaml` with `openapi3.NewLoader().LoadFromFile` and `doc.Validate(ctx)`:

```go
func TestOpenAPIConventions(t *testing.T) {
    doc := loadSpec(t) // fails test on load/validate error
    public := map[string]bool{"getHealth": true, "login": true}
    for path, item := range doc.Paths.Map() {
        for method, op := range item.Operations() {
            require.NotEmpty(t, op.OperationID, "%s %s", method, path)
            require.NotNil(t, op.Responses.Default(), op.OperationID)
            require.Equal(t, "#/components/responses/Error", op.Responses.Default().Ref, op.OperationID)
            if public[op.OperationID] {
                require.NotNil(t, op.Security); require.Empty(t, *op.Security)
            }
            if strings.HasPrefix(op.OperationID, "list") {
                require.ElementsMatch(t, []string{"items", "total"}, okSchema(t, op).Required, op.OperationID)
                require.True(t, hasParamRef(op, "Limit") && hasParamRef(op, "Offset"), op.OperationID)
            }
        }
    }
}
func TestEveryResourceOperationExists(t *testing.T) // asserts the set of operationIds equals the table above (45 ids, `streamLogs`, `compileAlias` and `cancelAccountLogin` included)
```

- [ ] **Step 2: Run, expect FAIL** — `go test ./internal/api -run 'TestOpenAPIConventions|TestEveryResourceOperationExists'` → FAIL (`no such file api/openapi.yaml`).
- [ ] **Step 3: Write `api/openapi.yaml`** per the tables above (`openapi: 3.1.0`, `info.title: remora management API`, `info.license: Apache-2.0`).
- [ ] **Step 4: Add the generator** — `go get -tool github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@latest`; `internal/api/gen/generate.go`:

```go
// Package gen holds code generated from api/openapi.yaml. Do not edit api.gen.go.
package gen

//go:generate go tool oapi-codegen -config ../../../api/oapi-codegen.yaml ../../../api/openapi.yaml
```

Run: `go generate ./internal/api/gen && go build ./...` → exit 0.
- [ ] **Step 5: Run tests, expect PASS** — `go test ./internal/api -run 'TestOpenAPI|TestEveryResource'` → `ok`.
- [ ] **Step 6: CI drift step** — in `.github/workflows/ci.yml`, job that runs Go tests, add after checkout/setup-go:

```yaml
      - name: OpenAPI generation drift
        run: go generate ./internal/api/gen && git diff --exit-code -- internal/api/gen
```

- [ ] **Step 7: Commit** — `git add api internal/api go.mod go.sum .github/workflows/ci.yml && git commit -m "feat(api): add the management OpenAPI contract and generated server"`

---

### Task 2: Admin authentication primitives

**Files:**
- Create: `internal/auth/password.go`, `session.go`, `token.go`, `limiter.go`, `middleware.go` (+ `_test.go` each), `internal/store/auth.go`, `internal/store/auth_test.go`, `internal/store/migrations/0005_auth.sql`

**Interfaces:**
- Consumes: `*store.Store`, `Store.Setting`/`SetSetting`, `auth.EnsureAdmin`, `auth.CheckAdminPassword` and
  `auth.SettingAdminPasswordHash` (plan 01). This plan adds no second `EnsureAdmin` and no settings accessors.
- Produces:

```go
// internal/store
type APIToken struct{ ID, Name, Prefix string; Hash []byte; CreatedAt time.Time; LastUsedAt *time.Time }
func (s *Store) CreateSession(ctx context.Context, hash []byte, expires time.Time) error
func (s *Store) SessionExpiry(ctx context.Context, hash []byte) (time.Time, error)
func (s *Store) DeleteSession(ctx context.Context, hash []byte) error
func (s *Store) DeleteSessionsExcept(ctx context.Context, keep []byte) error
func (s *Store) CreateAPIToken(ctx context.Context, t APIToken) error
func (s *Store) ListAPITokens(ctx context.Context) ([]APIToken, error)
func (s *Store) APITokenByHash(ctx context.Context, hash []byte) (APIToken, error)
func (s *Store) DeleteAPIToken(ctx context.Context, id string) error
func (s *Store) TouchAPIToken(ctx context.Context, id string, at time.Time) error

// internal/auth
const CookieName = "remora_session"
const SessionTTL = 7 * 24 * time.Hour
var ErrBadPassword, ErrWeakPassword, ErrUnauthenticated, ErrTooManyAttempts error
func HashPassword(pw string) (string, error)            // bcrypt, default cost; ErrWeakPassword if len<12 or >72 bytes
func CheckPassword(hash, pw string) bool
type Service struct{ /* store, clock, limiter */ }
func NewService(st *store.Store, now func() time.Time) *Service
func (s *Service) Login(ctx context.Context, remoteIP, password string) (token string, expires time.Time, err error) // checks with plan 01's CheckAdminPassword
func (s *Service) Logout(ctx context.Context, token string) error
func (s *Service) ChangePassword(ctx context.Context, currentSession, current, next string) error // HashPassword(next) (12-char minimum) into SettingAdminPasswordHash; revokes all other sessions
func (s *Service) CreateToken(ctx context.Context, name string) (store.APIToken, string, error)   // value "rmt_" + 32 base32 chars
func (s *Service) Authenticate(r *http.Request) (Principal, error)  // cookie session or "Authorization: Bearer rmt_…"
type Principal struct{ Via string } // "session" | "token"
func (s *Service) Middleware(public func(*http.Request) bool) func(http.Handler) http.Handler
// Middleware: http.NewCrossOriginProtection() first (Go 1.25 CSRF defence), then Authenticate unless public(r); 401 Error JSON on failure.
func SetSessionCookie(w http.ResponseWriter, r *http.Request, token string, expires time.Time)
func ClearSessionCookie(w http.ResponseWriter, r *http.Request)
```

Decisions: sessions and tokens are stored only as `sha256`; cookie `HttpOnly`, `SameSite=Strict`, `Path=/`, `Secure` unless the request is plain HTTP (no TLS and no `X-Forwarded-Proto: https`) so an admin on a LAN address without TLS is not locked out. CSRF stance: `SameSite=Strict` plus `http.CrossOriginProtection` (rejects cross-origin unsafe methods via `Sec-Fetch-Site`/`Origin`); bearer clients send neither header and pass. The limiter allows 5 failed logins per IP per 15 minutes, then `ErrTooManyAttempts` until the window ends. A `/v1` client key presented as a bearer on `/api` is just an unknown token → 401.

- [ ] **Step 1: Failing tests** (table-driven where listed):

```go
func TestHashPassword(t *testing.T)            // "short" → ErrWeakPassword; 73-byte → ErrWeakPassword; ok → CheckPassword true, wrong → false
func TestChangedPasswordSurvivesEnv(t *testing.T) // EnsureAdmin(env "first-password-12"); ChangePassword → "second-password"; EnsureAdmin(env "first-password-12") again (a restart) → login with "second-password" works, "first-password-12" fails
func TestLoginLogout(t *testing.T)             // wrong pw → ErrBadPassword; ok → token; Authenticate(cookie)=={Via:"session"}; after Logout → ErrUnauthenticated
func TestSessionExpires(t *testing.T)          // clock + SessionTTL + 1s → ErrUnauthenticated
func TestLoginLimiter(t *testing.T)            // 5 bad → 6th (even correct) ErrTooManyAttempts; other IP unaffected; after 15m ok
func TestChangePasswordRevokesOtherSessions(t *testing.T) // s1,s2; change via s1 → s1 valid, s2 ErrUnauthenticated
func TestAPIToken(t *testing.T)                // value has prefix "rmt_", len 36; store row has no plaintext (bytes.Contains false); Bearer → {Via:"token"}; LastUsedAt set; deleted → 401
func TestClientKeyRejectedOnAPI(t *testing.T)  // Bearer "sk-anything" → ErrUnauthenticated
func TestMiddleware(t *testing.T)              // cross-site POST with valid cookie (Sec-Fetch-Site: cross-site) → 403; same-origin POST → 200; public path no auth → 200; no auth → 401 JSON {"error":{"code":"unauthorized"}}
func TestSessionCookieFlags(t *testing.T)      // https req → Secure, HttpOnly, SameSite=Strict; plain http → !Secure
```

- [ ] **Step 2: Run, expect FAIL** — `go test ./internal/auth ./internal/store -run 'Auth|Password|Login|Session|Token|Middleware|ClientKey'` → build failure (undefined).
- [ ] **Step 3: Migration `0005_auth.sql`** — tables `sessions(hash BLOB PRIMARY KEY, expires_at INTEGER NOT NULL)`, `api_tokens(id TEXT PRIMARY KEY, name TEXT NOT NULL, prefix TEXT NOT NULL, hash BLOB NOT NULL UNIQUE, created_at INTEGER NOT NULL, last_used_at INTEGER)`.
- [ ] **Step 4: Implement** the signatures above. Plan 01's `serve` already calls `auth.EnsureAdmin`; nothing changes there.
- [ ] **Step 5: Run, expect PASS** — same command → `ok` for both packages; `go test -race ./internal/auth` → `ok`.
- [ ] **Step 6: Commit** — `git commit -m "feat(auth): add admin sessions, API tokens and the CSRF guard"`

---

### Task 3: Server skeleton, ports, errors, auth and system handlers

**Files:**
- Create: `internal/api/server.go`, `ports.go`, `adapters.go`, `errors.go`, `page.go`, `system.go`, `authn.go`, `unimplemented.go`, `fakes_test.go`, `server_test.go`
- Modify: `cmd/remora` serve wiring (mount `/api/`)

**Interfaces:**
- Consumes: `gen.StrictServerInterface` (Task 1), `auth.Service` (Task 2).
- Produces (later tasks fill the methods; port types are what plans 02–04's adapters return):

```go
// ports.go
type ProviderKey struct{ Name, Value string } // Value never serialised by handlers
type Provider struct{ Name, Kind, Type, BaseURL string; AllowPrivateNetwork bool; Keys []ProviderKey }
type ProviderTest struct{ OK bool; Latency time.Duration; Message string }
type Providers interface {
    List(ctx context.Context) ([]Provider, error)
    Get(ctx context.Context, name string) (Provider, error)
    Create(ctx context.Context, p Provider) (Provider, error)
    Update(ctx context.Context, p Provider) (Provider, error) // empty Keys = keep stored keys
    Delete(ctx context.Context, name string) error
    Test(ctx context.Context, name string) (ProviderTest, error)
}
type Quota struct{ Model string; RemainingFraction *float64; ResetAt *time.Time }
type Account struct{ ID, Provider, Email, Status, Message string; Enabled bool; CooldownUntil *time.Time; Quota []Quota; Models []string }
type Login struct{ State, URL, UserCode string }
type LoginStatus struct{ Status, Message string } // "wait" | "ok" | "error"
type Accounts interface {
    List(ctx context.Context, provider string) ([]Account, error)
    StartLogin(ctx context.Context, provider string) (Login, error)
    LoginStatus(ctx context.Context, state string) (LoginStatus, error)
    CompleteLogin(ctx context.Context, state, redirectURL string) error
    CancelLogin(ctx context.Context, state string) error
    SetEnabled(ctx context.Context, id string, enabled bool) error
    Delete(ctx context.Context, id string) error
    ResetCooldown(ctx context.Context, id string) error
    Import(ctx context.Context, provider string, credential []byte) (Account, error) // plan 03's accounts.Service.Import (uploads with UploadCredential)
}
type Cooling struct{ Target alias.Target; Until time.Time; Reason string }
type Aliases interface {
    List(ctx context.Context) ([]alias.Alias, error)
    Get(ctx context.Context, name string) (alias.Alias, error)
    Put(ctx context.Context, a alias.Alias, create bool) (alias.Alias, error) // ErrCycle/ErrDepth/ErrUnknownMember pass through
    Delete(ctx context.Context, name string) error
    Chain(ctx context.Context, name string) ([]alias.Target, error)           // compiled, before cooling
    Compile(ctx context.Context, a alias.Alias) ([]alias.Target, error)       // an unsaved alias against the stored ones; stores nothing
    Cooling(ctx context.Context) ([]Cooling, error)
}
type Key struct{ ID, Name, Prefix string; Allow []string; Enabled bool; CreatedAt time.Time }
type KeyPatch struct{ Name *string; Allow *[]string; Enabled *bool }
type Keys interface {
    List(ctx context.Context) ([]Key, error)
    Get(ctx context.Context, id string) (Key, error)
    Create(ctx context.Context, name string, allow []string, value string) (Key, string, error) // value "" → generated; returns the value
    Update(ctx context.Context, id string, p KeyPatch) (Key, error)
    SetValue(ctx context.Context, id, value string) error   // apply/import replacing a key's value
    MatchesValue(ctx context.Context, id, value string) (bool, error)
    Delete(ctx context.Context, id string) error
}
type System interface {
    Health(ctx context.Context) (ok bool, engines map[string]error)
    Version() (remora string, engines map[string]string)
}
type EngineNames interface { // Task 8 uses it
    KeyByVK(ctx context.Context, vkID string) (keyID, keyName string, ok bool)
    VKByKey(ctx context.Context, keyID string) (vkID string, ok bool)
    AliasByRule(ctx context.Context, ruleID string) (string, bool)
    RuleByAlias(ctx context.Context, name string) (string, bool)
    Target(ctx context.Context, engineProvider, engineModel string) alias.Target
    EngineFilter(ctx context.Context, provider, model string) (engineProviders, engineModels []string)
}

// server.go
type Deps struct {
    Providers Providers; Accounts Accounts; Aliases Aliases; Keys Keys; System System
    Names EngineNames; Logs LogSource /* Task 8 */; Tail *bifrost.Tail /* plan 03 */; Auth *auth.Service
    Config *config.Service /* Task 13 */; Log *slog.Logger
}
type Server struct{ d Deps }
var _ gen.StrictServerInterface = (*Server)(nil)
func New(d Deps) *Server
func (s *Server) Handler() http.Handler // mux: strict handler under /api + /api/logs/live (Task 11), wrapped in auth.Middleware(public = getHealth, login)

// errors.go
func writeError(ctx context.Context, w http.ResponseWriter, r *http.Request, err error) // strict ResponseErrorHandlerFunc
func toAPIError(err error) (status int, code, message string)
// store.ErrNotFound→404 not_found; store.ErrDuplicate→409 conflict; alias.ErrCycle/ErrDepth/ErrUnknownMember, auth.ErrWeakPassword, validation→400 invalid_request;
// auth.ErrBadPassword/ErrUnauthenticated→401; auth.ErrTooManyAttempts→429; anything else→502 engine_error with Scrub(err.Error()) (Task 8) and slog.Error of the raw error.

// page.go
func page[T any](items []T, limit, offset *int) ([]T, int) // defaults 50/0, limit capped at 200; returns slice and total
func matches(q *string, fields ...string) bool             // case-insensitive substring; nil q → true
```

`unimplemented.go` holds every strict method returning `501 not_implemented`; each later task deletes the methods it implements, and Task 13's final step asserts the file is gone.

- [ ] **Step 1: Failing tests** in `server_test.go`, using in-memory fakes from `fakes_test.go` (`fakeProviders`, `fakeAccounts`, `fakeAliases`, `fakeKeys`, `fakeSystem`, `fakeNames`, `fakeLogs`) and `newTestServer(t) (*httptest.Server, *http.Client /* logged in */)`:

```go
func TestHealthIsPublic(t *testing.T)     // GET /api/health no auth → 200 {"status":"ok","engines":{"front-door":"ok"}}; fakeSystem engine error → "degraded"
func TestVersion(t *testing.T)            // authed → 200 remora + engines
func TestLoginSetsCookie(t *testing.T)    // POST /api/auth/login bad → 401 code unauthorized; good → 204 + Set-Cookie remora_session; GET /api/auth/session → via "session"
func TestLogoutClearsSession(t *testing.T)
func TestProtectedNeedsAuth(t *testing.T) // GET /api/providers no auth → 401
func TestErrorMapping(t *testing.T)       // table: store.ErrNotFound→404 not_found, store.ErrDuplicate→409, alias.ErrCycle→400 invalid_request, errors.New("bifrost: dial tcp 127.0.0.1:18080")→502 engine_error and body has no "bifrost" and no "127.0.0.1"
func TestPage(t *testing.T)               // 120 items, limit nil → 50, offset 100 limit 50 → 20 items total 120, limit 1000 → 200
```

- [ ] **Step 2: Run, expect FAIL** — `go test ./internal/api -run 'Health|Version|Login|Logout|Protected|ErrorMapping|Page'` → build failure.
- [ ] **Step 3: Implement** server, error mapping (`gen.StrictHTTPServerOptions{RequestErrorHandlerFunc, ResponseErrorHandlerFunc}` both route to `writeError`), page helpers, `getHealth`, `getVersion`, `login` (sets cookie via `auth.SetSessionCookie`; the strict handler needs the `*http.Request`/`ResponseWriter`, so `login`/`logout` return a response type implementing `Visit…Response(w)` that writes the cookie — pass the request through a context value set by a small middleware), `logout`, `getSession`, and `adapters.go` mapping plans 01–04's names (table above) to the ports. Mount `Server.Handler()` at `/api/` in `cmd/remora serve`.
- [ ] **Step 4: Run, expect PASS** — same command → `ok`; `go vet ./...` → no output.
- [ ] **Step 5: Commit** — `git commit -m "feat(api): add the management server skeleton, error model and auth endpoints"`

---

### Task 4: Providers

**Files:** Create `internal/api/providers.go`, `internal/api/providers_test.go`; modify `unimplemented.go`.

**Interfaces:** Consumes `Providers` port. Produces `listProviders`, `createProvider`, `getProvider`, `updateProvider`, `deleteProvider`, `testProvider`.

- [ ] **Step 1: Failing tests**

```go
func TestProviderStatus(t *testing.T) // never tested → "unknown"; testProvider ok → "ok"; one of two models cooling → "degraded"; testProvider fails → "down"
func TestProvidersCRUD(t *testing.T) // create {name:"openrouter",kind:"builtin",type:"openrouter",keys:[{name:"main",value:"k1"},{name:"spare",value:"k2"}]} → 201 key_count 2, status "unknown", keys [{name:"main"},{name:"spare"}], JSON has no "k1"; get → same; PUT without keys → fake still has both values; delete → 204; get → 404
func TestListProvidersFilters(t *testing.T) // 3 providers; ?kind=openai-compatible → 1; ?type=openrouter → 1; ?q=OR → case-insensitive name match; total counts filtered set
func TestCreateProviderValidation(t *testing.T) // table: empty name → 400; name with "/" → 400; kind "openai" (not canonical) → 400; builtin without type → 400; openai-compatible with a type → 400; openai-compatible or typesafe-compatible without base_url → 400; duplicate → 409
func TestTestProvider(t *testing.T) // fake returns {OK:false, Message:"401 from upstream"} → 200 {"ok":false,"message":"401 from upstream"}
```

- [ ] **Step 2: Run, expect FAIL** — `go test ./internal/api -run Provider` → FAIL (501).
- [ ] **Step 3: Implement.** Provider names match `^[a-z0-9][a-z0-9-]{0,62}$` (they prefix `<provider>/<model>` refs); `kind` is one of the four canonical values; `type` only and always for `builtin`; `base_url` required for `openai-compatible` and `typesafe-compatible`. The rest (a builtin's name equals its type, reserved names) is plan 02's reconcile validation, surfaced as 400.
- [ ] **Step 4: Run, expect PASS.**
- [ ] **Step 5: Commit** — `git commit -m "feat(api): add provider endpoints"`

---

### Task 5: Accounts (OAuth)

**Files:** Create `internal/api/accounts.go`, `accounts_test.go`.

**Interfaces:** Consumes `Accounts` port (plan 03's `accounts.Service`; `Import` is plan 03 Task 4's, which uploads with `UploadCredential`). Produces `listAccounts`, `startAccountLogin`, `getAccountLogin`, `completeAccountLogin`, `cancelAccountLogin`, `updateAccount`, `deleteAccount`, `resetAccountCooldown`, `importAccount`.

- [ ] **Step 1: Failing tests**

```go
func TestAccountLoginFlow(t *testing.T) // start {provider:"codex"} → {state:"s1", url:"https://…"}; status → "wait"; complete {redirect_url:"http://localhost:1455/auth/callback?code=c&state=s1"} → 202 and fake saw ("s1", that URL); status → "ok"
func TestDeviceCodeLogin(t *testing.T)  // fake Login{UserCode:"ABCD-1234"} → body user_code "ABCD-1234"
func TestCancelAccountLogin(t *testing.T) // DELETE /api/accounts/logins/s1 → 204 and fake CancelLogin("s1"); unknown state → 404
func TestCompleteLoginRejectsBadURL(t *testing.T) // table: "", "not a url", "http://localhost/cb" (no code, no error) → 400; fake not called
func TestCompleteLoginWithoutState(t *testing.T)  // "http://localhost:1455/cb?code=c" (no state) → 202; the service, not the handler, checks state when present
func TestListAccounts(t *testing.T)     // ?provider=antigravity filters; cooldown_until, quota, models serialised; ?q matches email
func TestAccountToggleDeleteReset(t *testing.T) // PATCH {enabled:false} → fake SetEnabled(id,false); reset-cooldown → 204; delete → 204; unknown id → 404
func TestImportAccount(t *testing.T)    // {provider:"antigravity", credential:{"type":"antigravity",…}} → fake got the exact JSON bytes; credential.type != provider → 400
```

- [ ] **Step 2: Run, expect FAIL** — `go test ./internal/api -run Account` → FAIL.
- [ ] **Step 3: Implement.** `redirect_url` must parse as an absolute URL whose query has `code` or `error`; `state` is optional (some providers omit it), and the account service verifies it against the login only when it is present; the engine accepts the full URL as `redirect_url` (`POST /v8/management/oauth/callback`, research map).
- [ ] **Step 4: Run, expect PASS.**
- [ ] **Step 5: Commit** — `git commit -m "feat(api): add OAuth account endpoints"`

---

### Task 6: Aliases, compiled chain, cooling

**Files:** Create `internal/api/aliases.go`, `aliases_test.go`.

**Interfaces:** Consumes `Aliases` port, `alias.Alias`, `alias.Target`. Produces `listAliases`, `createAlias`, `getAlias`, `updateAlias`, `deleteAlias`, `getAliasChain`, `compileAlias`, `listCooling`.

- [ ] **Step 1: Failing tests**

```go
func TestAliasesCRUD(t *testing.T)  // create {name:"fast",kind:"chat",members:["or/a","cheap"]} → 201; PUT changes members order → fake has new order; delete → 204
func TestAliasErrors(t *testing.T)  // fake Put returns alias.ErrCycle → 400 invalid_request message mentions "cycle"; ErrUnknownMember → 400; unknown kind → 400
func TestAliasChain(t *testing.T)   // Chain = [or/a, ag/b]; Cooling has ag/b until T → targets[1].cooling_until == T, targets[0] has none
func TestCompileAlias(t *testing.T) // POST /api/aliases/compile {name:"draft",kind:"chat",members:["fast","or/c"]} → 200 targets from fake Compile; fake stored nothing; Compile returns alias.ErrCycle → 400 invalid_request
func TestListCooling(t *testing.T)  // one cooling target used by aliases fast and smart → item.aliases == ["fast","smart"] (computed from List+Chain)
func TestListAliasesFilter(t *testing.T) // ?kind=embeddings; ?q
```

- [ ] **Step 2–4: FAIL → implement → PASS** — `go test ./internal/api -run 'Alias|Cooling'`.
- [ ] **Step 5: Commit** — `git commit -m "feat(api): add alias, chain and cooling endpoints"`

---

### Task 7: Keys

**Files:** Create `internal/api/keys.go`, `keys_test.go`.

**Interfaces:** Consumes `Keys` port. Produces `listKeys`, `createKey`, `getKey`, `updateKey`, `deleteKey` (`getKeyUsage` lands in Task 10).

- [ ] **Step 1: Failing tests**

```go
func TestCreateKeyShowsValueOnce(t *testing.T) // POST {name:"svc",allow:["fast","antigravity/*"]} → 201 value non-empty, not prefixed "sk-bf-"; GET /api/keys/{id} and list → no "value" field anywhere
func TestCreateKeyWithValue(t *testing.T)      // value "sk-imported-123" → fake stored exactly that; response value == it
func TestUpdateKey(t *testing.T)               // PATCH {enabled:false} only → allow unchanged; PATCH {allow:[]} → allow empty (deny all)
func TestKeyAllowValidation(t *testing.T)      // allow entry "[" (bad glob, path.Match ErrBadPattern) → 400
func TestDeleteKey(t *testing.T)
```

- [ ] **Step 2–4: FAIL → implement → PASS** — `go test ./internal/api -run Key`.
- [ ] **Step 5: Commit** — `git commit -m "feat(api): add client key endpoints"`

---

### Task 8: Front-door log client extensions and name translation

**Files:** Modify `internal/engine/bifrost/logs.go`, `logs_test.go` (plan 03 Task 8 created both); create `internal/api/translate.go`, `translate_test.go`.

**Interfaces:**
- Consumes: plan 03 Task 8's `bifrost.LogFilter`, `Log`, `LogPage`, `Client.SearchLogs`, `Client.GetLog` (404 → `store.ErrNotFound`) and `Log.Failure`; plan 02's `bifrost.Client.do(ctx, method, path string, in, out any) error`.
- Produces: the fields and methods below, **added** to plan 03's types. Nothing plan 03 defined is redefined or renamed; plan 03's fields (`LogFilter.Status`, `Start`, `Limit`, `SortBy`, `Order`; `Log.ID`, `ParentRequestID`, `Timestamp`, `Provider`, `Model`, `Status`, `ErrorDetails`) keep their names and query encoding.

```go
// internal/engine/bifrost/logs.go — verified against handlers/logging.go@72457fe
// LogFilter gains (query names in parentheses):
    Providers, Models, VirtualKeyIDs, RoutingRuleIDs []string // providers, models, virtual_key_ids, routing_rule_ids (comma-separated), logging.go:638-662
    End             *time.Time // end_time, RFC3339Nano (:699-708)
    ContentSearch   string     // content_search (:753)
    ParentRequestID string     // parent_request_id (:322)
    Offset          int        // offset
// Log gains (framework/logstore/tables.go:303-497):
    FallbackIndex int `json:"fallback_index"`; VirtualKeyID *string `json:"virtual_key_id"`; RoutingRuleID *string `json:"routing_rule_id"`
    Latency *float64 `json:"latency"`; Cost *float64 `json:"cost"`; Stream bool `json:"stream"`; TokenUsage *TokenUsage `json:"token_usage"`
    InputHistory json.RawMessage `json:"input_history"`; OutputMessage json.RawMessage `json:"output_message"`; Params json.RawMessage `json:"params"`
type TokenUsage struct{ PromptTokens, CompletionTokens, TotalTokens int64 } // json prompt_tokens, completion_tokens, total_tokens
type LogStats struct { // SearchStats, tables.go:250-263
    UserFacingTotalRequests int64 `json:"user_facing_total_requests"`; UserFacingSuccessRate float64 `json:"user_facing_success_rate"`
    PromptTokens, CompletionTokens, TotalTokens int64; TotalCost float64 `json:"total_cost"`
}
type HistogramBucket struct{ Timestamp time.Time; Count, Success, Error int64 } // tables.go:2589
func (c *Client) LogStats(ctx context.Context, f LogFilter) (LogStats, error)  // GET /api/logs/stats
func (c *Client) Histogram(ctx context.Context, f LogFilter) ([]HistogramBucket, error) // GET /api/logs/histogram (bucket size chosen by the engine from the range)

// internal/api/translate.go
type LogSource interface { // satisfied by *bifrost.Client
    SearchLogs(ctx context.Context, f bifrost.LogFilter) (bifrost.LogPage, error)
    GetLog(ctx context.Context, id string) (bifrost.Log, error)
    LogStats(ctx context.Context, f bifrost.LogFilter) (bifrost.LogStats, error)
    Histogram(ctx context.Context, f bifrost.LogFilter) ([]bifrost.HistogramBucket, error)
}
func Scrub(s string) string // case-insensitive: "cliproxyapi"/"cli proxy api"/"cliproxy" → "oauth engine", "bifrost" → "gateway", "sk-bf-[A-Za-z0-9_-]+" → "[redacted]", "127.0.0.1:<port>"/"localhost:<port>" → "upstream"
func (s *Server) toEntry(ctx context.Context, l bifrost.Log, rootAlias string) gen.LogEntry // key via Names.KeyByVK, alias via Names.AliasByRule (else rootAlias), provider/model via Names.Target, error = Scrub(error_details.error.message)
func (s *Server) toFilter(ctx context.Context, p gen.ListLogsParams) (bifrost.LogFilter, error) // remora names → engine ids; unknown key/alias → empty result, not an error
```

Facts this relies on (Bifrost `72457fe`): a fallback attempt is its own row with `parent_request_id` = the primary request id and `fallback_index` = attempt (`plugins/logging/main.go:2020-2049`); rows carry `routing_rule_id`/`routing_rule_name` and `virtual_key_id` (`tables.go:321-326`). Bifrost's own `alias` column is its model-alias mapping, not a remora alias — remora aliases are routing rules, so alias attribution is `routing_rule_id` → alias. There is **no p50 anywhere** in the log store (only avg/p90/p95/p99 per histogram bucket), which drives Task 10.

- [ ] **Step 1: Failing tests**

```go
// bifrost/logs_test.go (plan 03's file; its TestSearchLogsQuery and TestGetLogFailure stay) — httptest server asserts the query string
func TestSearchLogsQueryAllFilters(t *testing.T) // filter{VirtualKeyIDs:["vk1","vk2"],Status:["error"],Start:t0,ContentSearch:"سلام‌دنیا",Limit:20,Offset:40,SortBy:"latency",Order:"asc"} → server sees virtual_key_ids=vk1,vk2&status=error&start_time=<RFC3339Nano>&content_search=<exact bytes incl. U+200C>&limit=20&offset=40&sort_by=latency&order=asc; decodes total_count
func TestLogStatsDecode(t *testing.T)
func TestHistogramDecode(t *testing.T)
// api/translate_test.go
func TestScrub(t *testing.T) // table: "bifrost: provider cliproxyapi returned 429" → no "bifrost"/"cliproxy"; "key sk-bf-abc123 invalid" → "[redacted]"; Persian text with ZWNJ unchanged byte-for-byte
func TestToEntry(t *testing.T) // fakeNames: vk1→key "svc", rule r1→"fast", ("oauth-internal","codex/gpt-x")→codex/gpt-x; row → {key:"svc",alias:"fast",provider:"codex",model:"gpt-x",attempt:0}; child row without routing_rule_id + rootAlias "fast" → alias "fast"
func TestToEntryNeverLeaksEngineProvider(t *testing.T) // every gen.LogEntry string field from a row naming the internal provider contains no engine name (reflect over fields)
```

- [ ] **Step 2–4: FAIL → implement → PASS** — `go test ./internal/engine/bifrost ./internal/api -run 'SearchLogs|LogStats|Histogram|Scrub|ToEntry'`.
- [ ] **Step 5: Commit** — `git commit -m "feat(bifrost): extend the log client with filters, stats and histogram; translate engine names"`

---

### Task 9: Logs list and detail

**Files:** Create `internal/api/logs.go`, `logs_test.go`.

**Interfaces:** Consumes `LogSource`, `EngineNames`, `toEntry`, `toFilter` (Task 8). Produces `listLogs`, `getLog`.

- [ ] **Step 1: Failing tests** (fakeLogs records the last `LogFilter`)

```go
func TestListLogsFilters(t *testing.T) // ?key=k1&alias=fast&provider=or&model=or/a&status=error&from=..&to=..&q=x → filter has VirtualKeyIDs [vk1], RoutingRuleIDs [r1], Providers/Models from EngineFilter, Status [error], Start/End, ContentSearch "x"
func TestListLogsUnknownAlias(t *testing.T) // ?alias=nope → 200 {items:[],total:0}, fakeLogs not called
func TestListLogsPaging(t *testing.T)  // limit/offset passed through; total from LogPage.Total
func TestGetLogFallbackPath(t *testing.T) // root r (attempt 0, error 429) + children c1 (attempt 1, error 402), c2 (attempt 2, success) via ParentRequestID filter → fallback_path ids [r,c1,c2], each with provider/model/status/error; alias on all = root's
func TestGetLogOfChildShowsWholePath(t *testing.T) // GET /api/logs/c1 → loads root via parent_request_id, same fallback_path
func TestGetLogBodies(t *testing.T) // input_history → request.messages, params → request.params, output_message → response.message; raw engine fields absent from JSON
```

- [ ] **Step 2–4: FAIL → implement → PASS** — `go test ./internal/api -run ListLogs|GetLog`. Path query: `SearchLogs{ParentRequestID: rootID, SortBy:"timestamp", Order:"asc", Limit: 50}`.
- [ ] **Step 5: Commit** — `git commit -m "feat(api): add log list and detail with the fallback path"`

---

### Task 10: Stats and key usage

**Files:** Create `internal/api/stats.go`, `stats_test.go`.

**Interfaces:** Consumes `LogSource`, `EngineNames`, port lists (`Keys.List`, `Aliases.List`, `Providers.List` + distinct `Accounts` providers). Produces `getStats`, `getKeyUsage`, and

```go
func percentile(ctx context.Context, src LogSource, f bifrost.LogFilter, p float64) (*float64, error)
func (s *Server) summary(ctx context.Context, f bifrost.LogFilter) (gen.StatsSummary, error)
```

Decisions:
- `requests` = `user_facing_total_requests`; `error_rate` = `1 - user_facing_success_rate/100` (a fallback chain counts once, as the user saw it); `errors` = `round(requests × error_rate)`; tokens and cost from `/api/logs/stats`.
- Percentiles are exact and cheap, using the list endpoint's latency sort: count `n` = `SearchLogs{f + Status:["success"], Limit:1}.Total`; `p` → `SearchLogs{…, SortBy:"latency", Order:"asc", Offset: ceil(p·n)-1, Limit:1}.Logs[0].Latency`. `n == 0` → `nil`. Over successful attempts.
- `series` from `Histogram` (time, count, error).
- `group_by=key|alias|provider`: one `summary` per member of that set from the store (keys, aliases, providers), sequentially; members with zero requests are omitted.

- [ ] **Step 1: Failing tests**

```go
func TestPercentile(t *testing.T)      // fake latencies [10..100 step 10], n=10 → p50 offset 4 → 50; p95 offset 9 → 100
func TestStatsEmptyRange(t *testing.T) // stats zeros, n=0 → 200 requests 0, error_rate 0, latency_p50_ms null, latency_p95_ms null, no NaN in body
func TestStatsSummary(t *testing.T)    // user_facing_total 200, success 97.5 → errors 5, error_rate 0.025; tokens/cost copied
func TestStatsGroupByAlias(t *testing.T) // aliases fast(r1), smart(r2) → two LogStats calls with RoutingRuleIDs [r1] and [r2]; groups named fast, smart
func TestStatsDefaultRange(t *testing.T) // no from/to → last 24h (fake clock)
func TestKeyUsage(t *testing.T)          // GET /api/keys/k1/usage → filter VirtualKeyIDs [vk1]; unknown key → 404
```

- [ ] **Step 2–4: FAIL → implement → PASS** — `go test ./internal/api -run 'Stats|Percentile|KeyUsage'`.
- [ ] **Step 5: Commit** — `git commit -m "feat(api): add stats with latency percentiles and per-key usage"`

---

### Task 11: Live log stream

**Files:** Create `internal/api/livelogs.go`, `livelogs_test.go`.

**Engine fact:** Bifrost's `/ws` (`handlers/websocket.go:93`) broadcasts only `store_update` and `notification` events at `72457fe`; nothing subscribes the logging plugin's `LogCallback` to it. So remora's live stream fans out plan 03's poller, `bifrost.Tail` (plan 03 Task 8; `serve` creates the one instance in plan 03 Task 10), over remora's own WebSocket. This task defines no poller.

**Interfaces:**

```go
// internal/api/livelogs.go
func (s *Server) streamLogs(w http.ResponseWriter, r *http.Request)
// mounted at GET /api/logs/live, behind auth.Middleware like every other /api route: the session cookie (SameSite=Strict,
// so a same-origin browser WebSocket sends it) or an API token. websocket.Accept with OriginPatterns = the request host only.
// Subscribes d.Tail with buffer 64 and unsubscribes on close; each server text frame is one JSON gen.LogEntry (toEntry, Task 8);
// optional ?key=&alias=&provider=&status= filters applied in remora. Client text frames are read and discarded, so the UI's
// "ping" keepalive is ignored.
```

When plan 03's `Tail` drops a subscriber whose buffer is full (it closes the channel), the socket is closed with status 1013 ("try again later"); the poller never blocks.

- [ ] **Step 1: Failing tests** (a fake `Tail` source feeding plan 03's `bifrost.NewTail`)

```go
func TestStreamLogsSessionAuth(t *testing.T)      // no cookie and no token → 401 before upgrade; session cookie → upgrade; Bearer API token → upgrade; message decodes to gen.LogEntry with remora names
func TestStreamLogsFilter(t *testing.T)           // ?alias=fast → only entries with alias fast
func TestStreamLogsIgnoresPing(t *testing.T)      // client sends "ping" ten times → socket stays open, no reply frame, entries still arrive
func TestStreamLogsSlowClientClosed(t *testing.T) // client never reads → socket closed with 1013; a second client still receives every entry
func TestStreamLogsRejectsForeignOrigin(t *testing.T) // Origin https://evil.example → 403
```

- [ ] **Step 2–4: FAIL → implement → PASS** — `go test -race ./internal/api -run 'StreamLogs'`.
- [ ] **Step 5: Commit** — `git commit -m "feat(api): stream live logs over a session-authenticated WebSocket"`

---

### Task 12: Settings: password and API tokens

**Files:** Create `internal/api/settings.go`, `settings_test.go`.

**Interfaces:** Consumes `auth.Service` (Task 2). Produces `changePassword`, `listApiTokens`, `createApiToken`, `deleteApiToken`.

- [ ] **Step 1: Failing tests**

```go
func TestChangePassword(t *testing.T)       // wrong current → 401; new of 11 chars → 400 invalid_request; 12 chars → 204, old pw login fails, new works, this session still valid
func TestApiTokenLifecycle(t *testing.T)    // create {name:"ci"} → value "rmt_…"; list → no value, prefix shown; token works as Bearer on GET /api/providers; delete → 401 with it
func TestTokenCannotChangePassword(t *testing.T) // Bearer token PUT /api/settings/password → 403 forbidden (only an interactive session may)
```

- [ ] **Step 2–4: FAIL → implement → PASS** — `go test ./internal/api -run 'Password|ApiToken'`.
- [ ] **Step 5: Commit** — `git commit -m "feat(api): add settings for the admin password and API tokens"`

---

### Task 13: `remora.yaml`: schema, export, plan, apply

**Files:** Create `api/remora.schema.json`, `internal/config/{export,plan,apply,secrets}.go` + tests; modify `internal/config/file.go` (plan 02: `Parse` also validates against the schema); `internal/api/config.go`, `config_test.go`; delete `internal/api/unimplemented.go`.

**Interfaces:**
- Consumes: ports `Providers`, `Aliases`, `Keys` (Task 3); plan 02's `remora.yaml` format, unchanged:
  `config.File{Version, Providers []FileProvider, Aliases []FileAlias, Keys []FileKey}`,
  `FileProvider{Name, Kind, Type, BaseURL, AllowPrivateNetwork, Keys []FileSecretKey{Name, Value}}`,
  `FileAlias{Name, Kind, Members, Capabilities, ContextWindow}`, `FileKey{Name, Value, Allow, Enabled *bool}`, and
  `config.Parse`. This task adds no document type of its own.
- Produces:

```go
// internal/config (extends plan 02's file.go)
type Change struct{ Op, Kind, Name string; Fields []string }
type Secrets map[string]string
// Parse (plan 02) additionally validates against the embedded api/remora.schema.json before decoding.
func Marshal(f File) ([]byte, error)              // stable order: each list sorted by name
func EnvName(provider, key string) string         // provider key "REMORA_PROVIDER_<PROVIDER>_<KEY>"; client key EnvName("", name) = "REMORA_KEY_<NAME>"; upper, non-alnum → "_"
type Service struct{ /* ports */ }
type Providers, Aliases, Keys interface{ /* exactly the Task 3 port method sets */ } // declared here; internal/api aliases them (type Providers = config.Providers) to avoid an import cycle, and Provider/Key/KeyPatch port structs move here with them
func NewService(p Providers, a Aliases, k Keys) *Service
func (s *Service) Export(ctx context.Context) (File, error)
func (s *Service) Plan(ctx context.Context, want File, sec Secrets, prune bool) ([]Change, error)
func (s *Service) Apply(ctx context.Context, want File, sec Secrets, prune bool) ([]Change, error) // returns changes applied; stops at the first failure and returns those plus the error
```

Decisions (the spec leaves the layout to the builder, "as long as export → apply is a round trip"):
- The layout is plan 02's; `api/remora.schema.json` describes exactly its fields (`version` 1, canonical provider `kind`s, `type` only for `builtin`, `env:` values only). Keys are matched by `name`, provider keys by provider + key name, providers and aliases by `name`. OAuth accounts are not in the document (credentials are not declarative); apply never touches them, and OAuth providers are not exported.
- Secret resolution: `env:NAME` → `sec[NAME]`. **Absent from `sec` → keep the stored value** (no change); absent for a resource being created → error `secret env:NAME is not set` naming the resource. Present → compared with the stored value (`Providers.Get().Keys`, `Keys.MatchesValue`) and a change only if different.
- Export writes every provider key as `{name, value: env:EnvName(provider, key)}` and no client key `value`. Plan 02's CLI rule (an unset env var is an error) becomes the plan rule above: unset keeps the stored secret.
- Order on apply: providers (create/update) → aliases in dependency order (members first) → keys; then, when `prune`, deletes in reverse: keys → aliases (dependents first) → providers.
- `Fields` lists changed field names (`members`, `allow`, `keys`, `base_url`, …) for the diff.
- The CLI (Task 14) fills `sec` from its own environment for every `env:` ref in the file; the UI import sends none, so it never changes a secret.

`internal/api/config.go`: `exportConfig` (YAML body, `Content-Disposition: attachment; filename=remora.yaml`), `planConfig`, `applyConfig` — each `Parse` the document (`400 invalid_request` with the schema error path on failure).

- [ ] **Step 1: Failing tests**

```go
func TestSchemaRejects(t *testing.T)        // table: version 2; alias kind "video"; provider kind "openai"; builtin without type; provider key value "plaintext" (not env:); unknown top-level field → Parse error mentions the path
func TestPlan02FixtureParses(t *testing.T) // Parse(test/e2e/testdata/remora.yaml) ok: plan 02's apply file is a valid document
func TestExportValidatesAgainstSchema(t *testing.T) // Export of a populated fake → Marshal → Parse ok; no secret value in the bytes
func TestRoundTripNoDiff(t *testing.T)      // fakes with 2 providers, nested aliases, 3 keys → Plan(Parse(Marshal(Export())), nil, true) == []Change{}
func TestRoundTripWithSecretsNoDiff(t *testing.T) // same, with sec holding the current values → no changes
func TestPlanSecretUnsetKeeps(t *testing.T) // existing provider, env unset → no change
func TestPlanSecretUnsetNewProvider(t *testing.T) // new provider "new" with key "main", env unset → error contains "env:REMORA_PROVIDER_NEW_MAIN"
func TestPlanDiff(t *testing.T)             // table: added alias → {create alias x}; reordered members → {update alias fast [members]}; removed key with prune → {delete key k}; without prune → none
func TestApplyOrder(t *testing.T)           // new provider p + alias a(member p/m) + alias b(member a) + key(allow b) → fake call log: provider p, alias a, alias b, key; prune deletes in reverse
func TestApplyStopsOnError(t *testing.T)    // second change fails → returns 1 applied change + error
func TestConfigEndpoints(t *testing.T)      // GET export → yaml; POST plan with invalid yaml → 400; POST apply → changes
func TestNoUnimplementedLeft(t *testing.T)  // every gen.StrictServerInterface method answers non-501 for a valid request (reflection over the generated interface)
```

- [ ] **Step 2–4: FAIL → implement → PASS** — `go test ./internal/config ./internal/api`.
- [ ] **Step 5: Commit** — `git commit -m "feat(config): add remora.yaml with export, plan and apply"`

---

### Task 14: `remora export` and `remora apply`

**Files:** Create `cmd/remora/client.go`, `export.go`; modify `cmd/remora/apply.go` and `apply_test.go` (plan 02 Task 9 created them as a direct-to-store command; this task turns `apply` into the API client below and keeps plan 02's file fields). Modify plan 02's e2e `TestMain` call to `remora apply -f /e2e/remora.yaml --yes`.

**Interfaces:**
- Consumes: `gen.ClientWithResponses` (Task 1), `config.Parse` (Task 13).
- Produces:

```go
func newClient(ctx context.Context, url, token, password string) (*gen.ClientWithResponses, error)
// url: --url, else REMORA_URL, else "http://127.0.0.1" + port of REMORA_LISTEN (default :8080)
// auth: --token / REMORA_TOKEN as Bearer; else REMORA_ADMIN_PASSWORD → POST /api/auth/login, cookie jar; else error "set REMORA_TOKEN (Settings → API tokens)"
func runExport(ctx context.Context, args []string, stdout io.Writer) error // remora export [-o FILE]
func runApply(ctx context.Context, args []string, stdout io.Writer, env func(string) (string, bool)) error
// remora apply -f FILE [--yes] [--prune=true]: Parse locally (fail fast), collect env refs → secrets, POST plan, print diff; no changes → "No changes."; without --yes → "Run again with --yes to apply." exit 0; with --yes → POST apply, print applied
func printChanges(w io.Writer, cs []gen.Change) // "+ provider openrouter", "~ alias fast (members)", "- key old"
```

- [ ] **Step 1: Failing tests** (`httptest` server built from `api.New` with fakes)

```go
func TestApplyWithoutYesChangesNothing(t *testing.T) // file adds alias → stdout "+ alias x" and "--yes"; fake aliases unchanged
func TestApplyYes(t *testing.T)                      // same with --yes → fake has alias x
func TestApplySendsOnlyReferencedEnv(t *testing.T)   // env has REMORA_PROVIDER_OR_KEY_1 and UNRELATED → request secrets == {REMORA_PROVIDER_OR_KEY_1: v}
func TestExportThenApplyNoChanges(t *testing.T)      // runExport → file → runApply → stdout "No changes."
func TestClientAuthOrder(t *testing.T)               // token wins over password; neither → error mentions REMORA_TOKEN
```

- [ ] **Step 2–4: FAIL → implement → PASS** — `go test ./cmd/remora -run 'Apply|Export|ClientAuth'`; `go build ./cmd/remora && ./remora apply --help` exits 0.
- [ ] **Step 5: Commit** — `git commit -m "feat(cli): add remora export and remora apply"`

---

### Task 15: OmniRoute reader and decryption

**Files:** Create `internal/importer/omniroute/decrypt.go`, `read.go`, `decrypt_test.go`, `read_test.go`, `fixture_test.go`.

**Interfaces:**

```go
// decrypt.go — mirrors OmniRoute src/lib/db/encryption.ts@9ecddd15
const prefix = "enc:v1:"                              // enc:v1:<iv_hex>:<ct_hex>:<tag_hex>
const staticSalt = "omniroute-field-encryption-v1"
type Decrypter struct{ keys [][]byte }
func NewDecrypter(secret string) *Decrypter          // keys: scrypt(secret, staticSalt), scrypt(secret, sha256(secret)[:16]) (legacy); scrypt N=16384 r=8 p=1 len 32 (Node scryptSync defaults)
func (d *Decrypter) Decrypt(v string) (string, error) // no prefix → v unchanged; AES-256-GCM, 16-byte IV (cipher.NewGCMWithNonceSize(block,16)), 16-byte tag, Open(iv, ct||tag); tries each key; ErrUndecryptable
func LoadSecret(dataDir string, env func(string) (string, bool)) ([]string, error)
// candidates in order: env STORAGE_ENCRYPTION_KEY; then STORAGE_ENCRYPTION_KEY= line in <dataDir>/server.env, then <dataDir>/.env (quotes stripped).
// For a file value containing a second "=", also return the JS-truncated form (text before it), because OmniRoute's own loader does split("=", 2)[1].
func PickSecret(cands []string, probe string) (*Decrypter, error) // first candidate that decrypts probe (any enc:v1 value from the DB); none → error "STORAGE_ENCRYPTION_KEY does not decrypt this database"

// read.go
type Node struct{ ID, Type, Name, Prefix, BaseURL string }                  // provider_nodes
type Connection struct{ ID, Provider, AuthType, Name, Email, ProjectID, APIKey, AccessToken, RefreshToken, IDToken, ExpiresAt string; ExpiresIn int; Active bool; Specific map[string]any } // provider_connections, secrets decrypted
type APIKey struct{ ID, Name, Key string; AllowedModels, AllowedCombos []string; AccessMode string; Revoked, Expired bool } // api_keys
type Combo struct{ Name string; Data json.RawMessage }                      // combos
type Snapshot struct{ Nodes []Node; Connections []Connection; Keys []APIKey; Combos []Combo }
func Read(ctx context.Context, dataDir string, env func(string) (string, bool), now time.Time) (Snapshot, error)
// opens <dataDir>/storage.sqlite as "file:<path>?mode=ro&immutable=1" (read-only; never writes, never a WAL checkpoint);
// SELECT * and maps by column name so missing optional columns (older OmniRoute) are tolerated; required: the columns of 001_initial_schema.sql.
```

Facts (OmniRoute `9ecddd15`): tables `provider_connections`, `provider_nodes`, `combos(name, data JSON)`, `api_keys(key plaintext)` (`src/lib/db/migrations/001_initial_schema.sql`); later columns `api_keys.revoked_at`, `expires_at`, `allowed_combos`, `allowed_models`, `model_access_mode ('all'|'restricted')` (migrations 032, 086, 147); custom OpenAI-compatible nodes have ids `openai-compatible-<apiType>-<id>` and connections reference them by that id (`src/app/api/provider-nodes/route.ts:190`). Only `api_key`, `access_token`, `refresh_token`, `id_token` are encrypted (`encryptConnectionFields`).

- [ ] **Step 1: Interop vector.** Generate once with Node and paste as constants into `decrypt_test.go` (test key only, never a real one):

```bash
node -e 'const c=require("crypto");const k=c.scryptSync("test-key-not-secret=","omniroute-field-encryption-v1",32);const iv=c.randomBytes(16);const x=c.createCipheriv("aes-256-gcm",k,iv);let e=x.update("hello سلام‌دنیا","utf8","hex");e+=x.final("hex");console.log(`enc:v1:${iv.toString("hex")}:${e}:${x.getAuthTag().toString("hex")}`)'
```

- [ ] **Step 2: Failing tests**

```go
func TestDecryptNodeVector(t *testing.T)   // NewDecrypter("test-key-not-secret=").Decrypt(nodeVector) == "hello سلام‌دنیا" (bytes equal, ZWNJ kept)
func TestDecryptLegacySalt(t *testing.T)   // value encrypted in-test with the legacy key decrypts
func TestDecryptPassthroughAndErrors(t *testing.T) // "plain" → "plain"; wrong key → ErrUndecryptable; "enc:v1:zz" malformed → error
func TestLoadSecretTruncation(t *testing.T) // server.env `STORAGE_ENCRYPTION_KEY="abc=def="` → cands ["abc=def=", "abc"]; env var wins and is first
func TestPickSecret(t *testing.T)          // probe encrypted with "abc" (truncated form) → picks "abc"
func TestReadFixture(t *testing.T)         // buildFixture(t, dir) (fixture_test.go: creates storage.sqlite with the four tables and rows encrypted under a test key; server.env) → Snapshot has decrypted tokens, revoked key flagged, allowed_models parsed
func TestReadIsReadOnly(t *testing.T)      // file mode 0400 + checksum before/after Read identical; no -wal/-shm created
func TestReadOlderSchema(t *testing.T)     // api_keys without allowed_combos/model_access_mode → AccessMode "all"
```

- [ ] **Step 3–5: FAIL → implement → PASS** — `go test ./internal/importer/omniroute -run 'Decrypt|LoadSecret|PickSecret|Read'`.
- [ ] **Step 6: Commit** — `git commit -m "feat(importer): read and decrypt an OmniRoute database read-only"`

---

### Task 16: OmniRoute conversion and `remora import omniroute`

**Files:** Create `internal/importer/omniroute/convert.go`, `credentials.go`, `convert_test.go`, `credentials_test.go`, `cmd/remora/import.go`, `cmd/remora/import_test.go`.

**Interfaces:**

```go
type Credential struct{ Provider, FileName string; JSON []byte }
type Result struct{ Doc config.File; Secrets config.Secrets; Credentials []Credential; Warnings []string }
func Convert(s Snapshot, now time.Time) Result
func antigravityFile(c Connection, now time.Time) (Credential, error) // sdk/auth/antigravity.go:309-343: type "antigravity", access_token, refresh_token, expires_in, timestamp (unix ms), expired (RFC3339), email, project_id; FileName "antigravity-<email>.json" (internal/auth/antigravity/filename.go:10-16)
func codexFile(c Connection) (Credential, error)                      // internal/auth/codex/token.go:227-247: type "codex", id_token, access_token, refresh_token, account_id (id_token claim "https://api.openai.com/auth".chatgpt_account_id, jwt_parser.go:22,43; payload decoded, not verified), email, expired, last_refresh, plan_type (chatgpt_plan_type); FileName "codex-<email>.json" (filename.go legacy form)
func runImport(ctx context.Context, args []string, stdout io.Writer, env func(string) (string, bool)) error
// remora import omniroute <data-dir> [--yes]: Read → Convert → print warnings → POST plan(doc, secrets, prune=false) → print diff + "<n> accounts: <provider> <email>…";
// without --yes stop; with --yes apply then POST /api/accounts/import per credential. Before Codex accounts print: "Codex refresh tokens are single-use: stop the old gateway before remora refreshes them."
```

Mapping rules:
- API-key connections (`auth_type` not `oauth`), grouped by provider → one remora provider with all active keys, named `key-1`, `key-2`, … in connection order. Provider name = the connection's provider id with kind `builtin` and `type` = that id; for `openai-compatible-*` ids → the node's `prefix` as name (so combo members `prefix/model` stay valid), kind `openai-compatible`, `base_url` from the node. Secret names via `config.EnvName`, values into `Secrets`.
- OAuth connections: `antigravity` and `codex` → credential files; any other OAuth provider → warning "skipped <provider> account <email>: not supported".
- Keys: name, `Value` = `env:REMORA_KEY_<NAME>` with the plaintext `key` verbatim in `Secrets`; revoked → skipped with warning; expired → `enabled: false`; `model_access_mode == "all"` → `allow: ["*"]`, else `allowed_models ∪ allowed_combos`.
- Combos → aliases: entries string (`a/b` → provider model, otherwise alias name), `{kind:"combo-ref", comboName}` → alias, `{model, providerId?}` → `model` if it has `/`, else `providerId/model`; `context_length` → `context_window`; `dimensions` set → kind `embeddings`, else `chat`; `strategy` other than `priority` → warning "alias <n>: strategy <s> becomes ordered fallback". Unknown member providers are kept verbatim; the server's plan rejects them with `ErrUnknownMember`, so nothing is silently dropped.

- [ ] **Step 1: Failing tests** (all on `buildFixture` from Task 15)

```go
func TestConvertProviders(t *testing.T)   // two openrouter connections → 1 provider {kind builtin, type openrouter}, keys [{key-1, env:REMORA_PROVIDER_OPENROUTER_KEY_1}, {key-2, …_KEY_2}], Secrets hold decrypted values; node prefix "oc" → provider "oc" kind openai-compatible with base_url
func TestConvertKeysVerbatim(t *testing.T) // key "sk-abc…" → Secrets["REMORA_KEY_SVC_A"] == exact value; revoked absent + warning; restricted → allow from allowed_models ∪ allowed_combos
func TestConvertCombos(t *testing.T)      // table over the three entry forms + combo-ref nesting; context_length → ContextWindow; dimensions → embeddings
func TestAntigravityFile(t *testing.T)    // JSON keys exactly {type,access_token,refresh_token,expires_in,timestamp,expired,email,project_id}; name "antigravity-a@example.com.json"
func TestCodexFile(t *testing.T)          // unsigned test JWT with chatgpt_account_id "acc-1", chatgpt_plan_type "plus" → account_id "acc-1", plan_type "plus"
func TestImportWithoutYes(t *testing.T)   // stdout lists "+ provider", "+ key", accounts; fakes untouched; no secret value in stdout
func TestImportYes(t *testing.T)          // fakes have providers, aliases, keys (fake key store holds the verbatim value), 2 imported credentials; existing remora alias not in OmniRoute still present (prune=false)
```

- [ ] **Step 2–4: FAIL → implement → PASS** — `go test ./internal/importer/omniroute ./cmd/remora -run 'Convert|File|Import'`.
- [ ] **Step 5: Commit** — `git commit -m "feat(importer): add remora import omniroute"`

---

### Task 17: End-to-end: login, configure, call `/v1`, export round trip

**Files:** Create `test/e2e/management_test.go` (build tag `e2e`).

**Interfaces:** Consumes plan 01's e2e helpers (`remoraURL`, `resetFakes`; compose sets `REMORA_ADMIN_PASSWORD: e2e-admin-password`), the fake upstream base URL `http://fakes:9090/u/mgmt` as seen from remora, and plan 02's `/v1` proxy.

- [ ] **Step 1: Write the test**

```go
//go:build e2e
func TestManagementEndToEnd(t *testing.T) {
    c := loginClient(t)                         // POST remoraURL("/api/auth/login") with "e2e-admin-password" → cookie jar
    mustStatus(t, c.post("/api/providers", `{"name":"fakeoai","kind":"openai-compatible","base_url":"`+fakeURL+`","allow_private_network":true,"keys":[{"name":"main","value":"fake-key"}]}`), 201)
    mustStatus(t, c.post("/api/aliases", `{"name":"e2e-fast","kind":"chat","members":["fakeoai/gpt-test"]}`), 201)
    key := createKey(t, c, "e2e", []string{"e2e-fast"})   // value from KeyCreated
    resp := chat(t, key, "e2e-fast", "سلام‌دنیا")          // POST /v1/chat/completions
    require.Equal(t, 200, resp.StatusCode)
    require.Equal(t, "e2e-fast", resp.Header.Get("x-remora-alias"))
    require.Equal(t, http.StatusForbidden, chatStatus(t, key, "fakeoai/other")) // not in the key's allowlist; use plan 02's refusal status if it differs
    eventually(t, 10*time.Second, func() bool { return logCount(t, c, "alias=e2e-fast") >= 1 })
    entry := firstLog(t, c, "alias=e2e-fast")
    require.Equal(t, "fakeoai", entry.Provider); require.Equal(t, "e2e", entry.Key)
    require.NotContains(t, strings.ToLower(rawLogs(t, c)), "bifrost")
    plan := planExported(t, c)                  // GET export → POST plan → 0 changes
    require.Empty(t, plan.Changes)
}
```

- [ ] **Step 2: Run** — `make image fakes-image && go test -tags e2e ./test/e2e -run TestManagementEndToEnd -v` (plan 01's `TestMain` brings the stack up) → `--- PASS: TestManagementEndToEnd`.
- [ ] **Step 3: Commit** — `git commit -m "test(e2e): drive the management API from login to a /v1 call"`

---

## Self-review notes

- Spec coverage: every row of the spec's management API table maps to Tasks 3–13; authentication → Task 2; export/apply/import → Tasks 13–16; codegen and CI drift → Task 1; UI client generation is plan 06's.
- Deliberately not here: budgets and rate limits per key (non-goal); Bifrost's own `alias` log filter (not remora's aliases); raw request/response bodies (engine `raw_request`/`raw_response` never exposed).
