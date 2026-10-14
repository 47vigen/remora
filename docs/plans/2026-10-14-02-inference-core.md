# Inference Core Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Clients call remora's `/v1/*` with a remora key and reach API-key providers through the front door,
with aliases, fallback inside one request, per-key allowlists, `/v1/models`, and a store that the engine always matches.

**Architecture:** remora owns keys, providers, aliases and their compilation, and keeps them in SQLite with secrets
sealed at rest. A reconciler pushes that state into the front door (Bifrost) over its management API: one provider
per remora provider, one virtual key per remora key, one routing rule per alias (first member as target, the rest
as fallbacks). The `/v1` proxy authenticates the remora key, checks the model as the client wrote it, swaps in the
internal virtual key, streams the engine's answer back unbuffered, and rewrites only headers and error envelopes.

**Tech Stack:** Go 1.27 (the engines' own floor: `go 1.27.0` in Bifrost `core/go.mod:3` and `transports/go.mod:3`), `net/http` + `net/http/httputil.ReverseProxy`, `database/sql` on SQLite (plan 01's
driver), `crypto/aes` GCM + `crypto/hkdf`, `github.com/coder/websocket` (WebSocket relay), `go.yaml.in/yaml/v3`
(the `apply` file), Docker Compose for e2e.

**Spec:** `docs/specs/2026-10-14-remora-0.1-design.md` (sections "Inference", "Keys", "Aliases", "`/v1/models`",
"Providers", "Reconciliation", "Source of truth and GitOps"). Shared names: `docs/plans/README.md`.

## Global Constraints

- Go module `github.com/47vigen/remora`, Go 1.27. Engines are never imported; remora talks to them over loopback HTTP.
- One exposed listener. No engine name, port, error format, header or credential reaches a user (anti-goal
  "Leaking the engines"). Engine prefixes such as `sk-bf-` never reach a user.
- Bodies are streamed through; remora never buffers a response stream or rewrites content. Persian text with ZWNJ
  (U+200C) is compared byte for byte.
- The allowlist is checked against the model **as the client wrote it** (alias or `<provider>/<model>`, globs).
- `/v1/models` lists aliases only, with `context_length` and `supported_parameters`; never a provider catalogue.
- Alias nesting allowed, cycles rejected, depth capped (default 5), chain flattened with duplicates removed.
- Fallback triggers: 429, 402, `insufficient_quota`, 5xx, transport errors. The engine falls back on any error
  except cancellation (`core/bifrost.go:5596-5631`); there is no per-trigger setting, so remora sets
  `network_config.max_retries: 0` so a 429 moves on at once.
- Unknown request parameters are never dropped silently.
- The store is the source of truth: store wins, and a failed engine call rolls the store change back.
- Headers remora adds: `x-remora-alias`, `x-remora-served-by`, `x-remora-fallback` (`true`/`false`).
- No real provider and no real key in any test. No owner host, domain, product name or secret in the repo.
- Go practice: `context.Context` first, errors wrapped with `%w`, table-driven tests, `log/slog`, no global state,
  small files. Conventional Commits with scopes.

## Review Focus

1. **A client smuggles its own fallbacks.** The engine reads a `fallbacks` array from the JSON body
   (`handlers/inference.go:78`), which would route past the allowlist. Expected: remora answers 400
   `fallbacks_not_supported` and nothing reaches the engine. Test in Task 5 (`TestExtractModel/fallbacks_field_rejected`).
2. **A client sends engine control headers** (`x-bf-vk`, `x-bf-passthrough-extra-params`, `x-bf-compat`) or its own
   `Authorization`. Expected: every inbound `x-bf-*`, `x-bifrost-*`, `Authorization`, `x-api-key` and `Cookie` is
   removed, and only remora's values go upstream. Test in Task 6 (`TestProxy_StripsClientEngineHeaders`).
3. **An engine error or header names the engine** (`is_bifrost_error`, `x-bifrost-*`, a message with "bifrost",
   a `sk-bf-` value), including an error event in the middle of an SSE stream. Expected: an error in the endpoint's own shape, engine
   words replaced, no `x-bifrost-*` header. Tests in Task 6 and the e2e leak guard in Task 11.
4. **The `model` field is not where a naive parser looks**: multipart with `model` after a large `file` part, JSON
   with `model` missing or not a string, a body over the size cap. Expected: the model is found without changing a
   byte of the body; a missing model is 400; an oversized body is 413. Tests in Task 5.
5. **An alias name contains a slash** (`memory/fast`, as in the reference deployment). Expected: it is an alias, not
   a provider model, everywhere: allowlist, compilation, routing rule, `/v1/models`. Tests in Task 3 and Task 11.

## Engine facts this plan relies on (Bifrost at `72457fe`, paths under `transports/bifrost-http/`)

| fact | source |
|---|---|
| Providers: `GET/POST /api/providers`, `PUT/DELETE /api/providers/{p}`; keys: `GET/POST /api/providers/{p}/keys`, `PUT/DELETE .../keys/{key_id}` | `handlers/providers.go:219-228` |
| Create body: `provider`, `network_config{base_url,max_retries,default_request_timeout_in_seconds,stream_idle_timeout_in_seconds,allow_private_network}`, `concurrency_and_buffer_size{concurrency,buffer_size}`, `custom_provider_config{base_provider_type,is_key_less}`; update needs both config objects non-zero | `handlers/providers.go:147-172`; `core/schemas/provider.go:64-78,563-571` |
| Custom provider name must not be a standard provider; base types include `openai` and `typesafe` | `handlers/providers.go:358-371`; `core/schemas/bifrost.go:86-133` |
| A private-network base URL needs `allow_private_network: true` (the e2e fakes are on a compose network) | `handlers/providers.go:398-413` |
| Key body: `id` (generated when empty), `name`, `value`, `models` (`["*"]` = all; empty denies), `weight`, `enabled`; list returns `{"keys":[...]}` | `core/schemas/account.go:33-58,137-164`; `handlers/provider_keys.go:31,192` |
| Virtual keys: `GET/POST /api/governance/virtual-keys`, `PUT/DELETE .../{vk_id}`; body `name`, `description`, `is_active`, `allow_all_providers`; create answers `{"virtual_key":{...}}` whose `value` is `{"value":"sk-bf-..."}`; list answers `{"virtual_keys":[...]}` | `handlers/governance.go:371-397,1806-1814,1938-1943,2319`; `core/schemas/secretvar.go:320-326` |
| Inference reads the virtual key from header `x-bf-vk` | `core/utils.go:489` |
| Routing rules: `GET/POST /api/routing/rules`, `GET/PUT/DELETE /api/routing/rules/{rule_id}`; body `name`, `description`, `enabled`, `cel_expression`, `targets[]{provider,model,weight}`, `fallbacks[]{provider,model}`, `scope`, `priority`; create answers `{"rule":{"id",...}}`; list answers `{"rules":[...]}` | `handlers/routing.go:119-123,138-160,712-715`; `framework/configstore/tables/routingrules.go:100-106` |
| CEL variables: `model`, `provider`, `request_type`, `headers`, `params` | `plugins/routing/rules/engine.go:648-654` |
| A bare name parses to provider `""` on native routes and Anthropic, `openai` on the Responses WebSocket, `typesafe` on `/typesafe/v1/systemone` | `core/schemas/utils.go:99-109`; `handlers/inference.go:57-64`; `handlers/wsresponses.go:198`; `core/providers/typesafe/decision.go:434` |
| Inference routes: `/v1/chat/completions`, `/v1/responses` (POST, and GET WebSocket upgrade), `/v1/embeddings`, `/v1/audio/transcriptions`, `/v1/decisions`; Anthropic at `/anthropic/v1/messages`; System One at `/typesafe/v1/systemone` | `handlers/inference.go:871-890`; `handlers/wsresponses.go:79`; `integrations/anthropic.go:124,1732`; `integrations/typesafe.go:24,34` |
| Unknown JSON params pass only with request header `x-bf-passthrough-extra-params: true` | `lib/ctx.go:741-745` |
| Response headers: `x-bifrost-routing-info-provider`, `-model`, `-is-fallback` (also set on streams before the first write) | `lib/responseheaders.go:85-99,110-126` |
| Native error body: `{"is_bifrost_error":..,"status_code":..,"error":{"type","code","message","param"},"extra_fields":..}`; `/typesafe` answers `{"detail":{"error_type","message"}}`; `/anthropic` answers Anthropic's shape | `core/schemas/bifrost.go:2116-2125,2242-2249`; `core/providers/typesafe/errors.go:120-165` |
| Health: `GET /health` → `{"status":"ok",...}`; management auth: HTTP Basic | `handlers/health.go:27-34`; `handlers/middlewares.go:1741-1752` |
| Provider defaults: concurrency 1000, buffer 5000 | `core/schemas/provider.go:13-21,257-258` |

## Corrections found during implementation

Checked against the pinned front door (transports/v2.3.0, 9237ec0) and the real binary; these override the text above
where they differ.

- Routing-rule priority is unique per scope (a duplicate answers 409). Every rule remora pushes takes its own priority
  at or above `RulePriorityBase = 1000`, keeps it across updates, and never renumbers another; below 1000 is left to
  the `remora-oauth-*` rules.
- A key update needs the key value (an empty value answers 400), so the reconciler always sends the decrypted secret.
  Key values starting with `env.` or `vault.` are refused before any call: the engine would resolve them itself.
- A provider needs a key before it can be a rule target or fallback; desired-state validation checks this.
- A virtual key's `value` is a plain string on create and list; the client also accepts the object form.
- Key ids and names are unique across providers (remora sends its key id as both); rule names are not unique, so the
  reconciler dedupes them. Deleting an unknown provider answers 200; updating an unknown provider creates it.
- Management errors are nested: `{"is_bifrost_error":false,"status_code":N,"error":{"message":..}}`.
- The front door's real mid-stream error is nested under `"error"`; both forms are mapped. Error bodies over 1 MiB are
  replaced with remora's own error. `Expect`, `Server`, the front door's CORS headers and any header naming the engine
  are dropped.
- `serve` sets up the sealer before the engines start (a bad master key fails fast), runs the first Sync before the
  listener opens (a wiped engine never meets a `/v1` request first), then reconciles on the timer. The management
  client has a 30 s timeout and ignores proxy environment variables.
- The Responses WebSocket checks every client frame the engine could read as `response.create` (its decoder matches
  keys case-insensitively); mapped engine error events carry `"type":"error"`, and remora's own carry `"status"`.

## Decisions

- **Seeding before plan 05 (the `remora apply -f` minimal form).** Task 9 adds `remora apply -f remora.yaml` as a
  real, shipped command, not a test-only path: it makes the store match the file and reconciles the engine. The e2e
  suite runs it inside the container of the image under test (`docker compose exec remora remora apply -f ...`),
  so CI tests the released image with no build tag and no backdoor. Plan 05 keeps these file fields (so export →
  apply stays a round trip) and adds the diff, export and API. A build-tagged `seed` command was rejected: the
  released image would not contain it, so e2e would test a different binary.
- **Cross-process safety.** `apply` runs as a second process on the same database. Every reconcile pass runs inside
  one SQLite `BEGIN IMMEDIATE` transaction (`store.Write`), so the server's timer and `apply` serialise on the
  database lock and never interleave engine pushes. The proxy only reads (WAL), and caches nothing, so no
  invalidation is needed.
- **Secrets at rest.** AES-256-GCM, key derived with HKDF-SHA256 (info `remora/secrets/v1`) from a 32-byte master
  key. The master key is `REMORA_MASTER_KEY` (base64, 32 bytes) when set, else `$REMORA_DATA/master.key` (mode
  0600) generated at first boot. It is kept out of `remora.db`, so a leaked database file or copy alone reveals no
  secret. AAD binds each ciphertext to its row (`provider_key:<id>`, `client_key:<id>`). Client key lookup uses
  `HMAC-SHA256(macKey, value)` (macKey from HKDF info `remora/keyhash/v1`), never a plain hash, so imported
  low-entropy key values cannot be brute-forced from the database alone.
- **Ownership of engine objects.** The engine is private, so the store wins over everything in it: providers not in
  the store (and not internal), virtual keys and routing rules remora does not know are deleted. Names carry the
  link: virtual key `remora:key:<client_key_id>`, routing rule `remora:alias:<alias>`, provider key id = remora
  provider key id. The engine ids are also persisted (`client_keys.vk_id`, `aliases.engine_rule_id`) because the
  engine's logs attribute requests by `virtual_key_id` and `routing_rule_id`; plan 05 joins logs on them (the
  engine's own log `alias` column is its model mapping, not a remora alias). A table `engine_sync` remembers an HMAC of each object as last pushed, so a timer pass makes no
  calls when nothing changed.
- **Alias routing rule.** CEL `model == "<name>" && provider in ["", "openai", "typesafe"]` (the three default
  providers a bare name parses to on the surfaces remora exposes), scope global, and a priority of its own at or above
  `RulePriorityBase` (1000; the engine refuses two rules with one priority in a scope). Alias names match
  `^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$`, and the part before a `/` must not be a provider name, so the engine
  never splits an alias as a provider model.
- **Allowlist globs.** `Allowed` keeps the `path.Match` contract and adds two rules users need: the pattern `*`
  matches every name, and a pattern ending in `/*` matches any depth below it (`openrouter/*` matches
  `openrouter/qwen/qwen3-embedding`). Raised as a contract refinement in the hand-off.
- **Error shapes.** Errors remora creates, and engine errors in the native envelope (`is_bifrost_error` present),
  are returned in the endpoint's own shape: `{"error":{"message","type","code","param"}}`, except on `/v1/messages`
  and `/v1/systemone`, where they use Anthropic's and TypeSafe's shapes. The engine already answers there in those
  shapes for their SDKs; remora passes those through with engine words scrubbed.
- **What is not rewritten.** The engine adds `extra_fields` to OpenAI-shaped bodies. It names no engine and carries
  no credential; stripping it would mean re-encoding every chunk, which the spec forbids. The e2e leak guard
  (Task 11) proves no engine name or `sk-bf-` reaches a client.

---

### Task 1: Front-door management client

**Files:**
- Create: `internal/engine/bifrost/client.go` (Client, New, do, APIError, ErrNotFound, Health)
- Create: `internal/engine/bifrost/providers.go` (providers and keys)
- Create: `internal/engine/bifrost/vkeys.go`
- Create: `internal/engine/bifrost/rules.go`
- Create: `internal/engine/bifrost/standard.go` (`IsStandardProvider`)
- Test: `internal/engine/bifrost/client_test.go`

**Interfaces:**
- Consumes: plan 01 — the front door's loopback base URL and its admin user/password from `engine_state`.
- Produces (consumed by Task 4, and by plans 03, 04, 05):

```go
package bifrost

var ErrNotFound = errors.New("bifrost: not found")
type APIError struct{ Status int; Message string } // Error() never reaches a client: the proxy maps errors itself

type Provider struct {
    Name                string
    BaseType            string // "" = a standard provider; "openai" | "typesafe" = custom provider on that base
    BaseURL             string
    AllowPrivateNetwork bool
    AllowedRequests     []string      // custom_provider_config.allowed_requests as {"<type>": true}; nil = omitted (all allowed). Plans 03, 04
    RequestTimeout      time.Duration // network_config.default_request_timeout_in_seconds; 0 = 600 s. Plan 04 sets the adapter deadline + 60 s
}
type Key struct{ ID, Name, Value string; Enabled bool }   // Value write-only; lists leave it empty
type VirtualKey struct{ ID, Name, Value string; Active bool }
type Route struct{ Provider, Model string } // in a rule target, Model "" is sent as null: the incoming model (plan 03)
type RoutingRule struct{ ID, Name, CEL string; Target Route; Fallbacks []Route }

func New(baseURL, user, password string, hc *http.Client) *Client
func (c *Client) Health(ctx context.Context) error
func (c *Client) ListProviders(ctx context.Context) ([]Provider, error)
func (c *Client) CreateProvider(ctx context.Context, p Provider) error
func (c *Client) UpdateProvider(ctx context.Context, p Provider) error
func (c *Client) DeleteProvider(ctx context.Context, name string) error
func (c *Client) ListKeys(ctx context.Context, provider string) ([]Key, error)
func (c *Client) CreateKey(ctx context.Context, provider string, k Key) error
func (c *Client) UpdateKey(ctx context.Context, provider string, k Key) error
func (c *Client) DeleteKey(ctx context.Context, provider, id string) error
func (c *Client) ListVirtualKeys(ctx context.Context) ([]VirtualKey, error)
func (c *Client) CreateVirtualKey(ctx context.Context, v VirtualKey) (VirtualKey, error) // returns ID and Value
func (c *Client) UpdateVirtualKey(ctx context.Context, v VirtualKey) error
func (c *Client) DeleteVirtualKey(ctx context.Context, id string) error
func (c *Client) ListRoutingRules(ctx context.Context) ([]RoutingRule, error)
func (c *Client) CreateRoutingRule(ctx context.Context, r RoutingRule) (RoutingRule, error) // returns the engine's ID
func (c *Client) UpdateRoutingRule(ctx context.Context, r RoutingRule) error
func (c *Client) DeleteRoutingRule(ctx context.Context, id string) error
func IsStandardProvider(name string) bool // copy of core/schemas/bifrost.go:98-133; Task 11 catches drift on bump
const OAuthProvider = "remora-oauth" // the OAuth engine's front-door provider (plan 03); its rules are "remora-oauth-<name>"
```

Wire values the client always sends: `network_config.max_retries: 0`, `default_request_timeout_in_seconds: 600`,
`stream_idle_timeout_in_seconds: 300`, `concurrency_and_buffer_size: {1000, 5000}`, key `models: ["*"]`,
`weight: 1`; virtual key `allow_all_providers: true`, `description: "managed by remora"`; rule `enabled: true`,
`scope: "global"`, a unique `priority` (see "Corrections found during implementation"), target `weight: 1`.

- [ ] **Step 1: Write the failing tests** — one `httptest.Server` that records method, path, Basic auth and decoded
  JSON body, and serves canned answers in the engine's shapes from the table above.

```go
func TestClient_Requests(t *testing.T) {
    cases := []struct {
        name       string
        call       func(c *Client) error
        method     string
        path       string
        wantBody   map[string]any // subset match on the decoded JSON
    }{
        {"custom openai provider", func(c *Client) error {
            return c.CreateProvider(ctx, Provider{Name: "up-a", BaseType: "openai", BaseURL: "http://fakes:9090/u/a", AllowPrivateNetwork: true})
        }, "POST", "/api/providers", map[string]any{
            "provider": "up-a",
            "custom_provider_config": map[string]any{"base_provider_type": "openai", "is_key_less": false},
            "network_config": map[string]any{"base_url": "http://fakes:9090/u/a", "max_retries": 0.0,
                "allow_private_network": true, "default_request_timeout_in_seconds": 600.0,
                "stream_idle_timeout_in_seconds": 300.0},
            "concurrency_and_buffer_size": map[string]any{"concurrency": 1000.0, "buffer_size": 5000.0}}},
        {"custom typesafe provider", /* BaseType "typesafe" */ ..., "POST", "/api/providers",
            map[string]any{"custom_provider_config": map[string]any{"base_provider_type": "typesafe"}}},
        {"standard provider has no custom config", /* Name "openrouter", BaseType "" */ ..., "POST", "/api/providers",
            map[string]any{"provider": "openrouter"}}, // and assert "custom_provider_config" absent
        {"update provider", ..., "PUT", "/api/providers/up-a", map[string]any{"network_config": map[string]any{"max_retries": 0.0}}},
        {"allowed requests and timeout", /* Provider{Name "soniox", BaseType "openai", AllowedRequests ["transcription"], RequestTimeout 1860*time.Second} */ ...,
            "POST", "/api/providers", map[string]any{
            "custom_provider_config": map[string]any{"allowed_requests": map[string]any{"transcription": true}},
            "network_config": map[string]any{"default_request_timeout_in_seconds": 1860.0}}},
        {"create key", ..., "POST", "/api/providers/up-a/keys", map[string]any{"id": "k1", "name": "main",
            "value": "test-upstream-key", "models": []any{"*"}, "weight": 1.0, "enabled": true}},
        {"delete key", ..., "DELETE", "/api/providers/up-a/keys/k1", nil},
        {"create virtual key", ..., "POST", "/api/governance/virtual-keys", map[string]any{
            "name": "remora:key:c1", "allow_all_providers": true, "is_active": true}},
        {"create rule", func(c *Client) error {
            return c.CreateRoutingRule(ctx, RoutingRule{Name: "remora:alias:fast", CEL: `model == "fast"`,
                Target: Route{"up-a", "m1"}, Fallbacks: []Route{{"up-b", "m1"}}})
        }, "POST", "/api/routing/rules", map[string]any{"name": "remora:alias:fast", "cel_expression": `model == "fast"`,
            "enabled": true, "scope": "global",
            "targets":   []any{map[string]any{"provider": "up-a", "model": "m1", "weight": 1.0}},
            "fallbacks": []any{map[string]any{"provider": "up-b", "model": "m1"}}}},
        {"update rule", ..., "PUT", "/api/routing/rules/r1", nil},
        {"rule target with incoming model", /* Target Route{"remora-oauth", ""} */ ..., "POST", "/api/routing/rules",
            map[string]any{"targets": []any{map[string]any{"provider": "remora-oauth", "model": nil, "weight": 1.0}}}},
    }
    // each: assert method, path, Basic auth "admin:secret", Content-Type application/json, body subset
}

func TestClient_CreateVirtualKeyReturnsIDAndValue(t *testing.T) // server answers {"virtual_key":{"id":"vk1","value":{"value":"sk-bf-x"}}} → VirtualKey{ID:"vk1", Value:"sk-bf-x"}
func TestClient_CreateRoutingRuleReturnsID(t *testing.T)        // server answers {"rule":{"id":"r1","name":"remora:alias:fast",...}} → RoutingRule.ID == "r1"
func TestClient_ListsDecode(t *testing.T)    // {"providers":[{"name":"up-a","network_config":{"base_url":"u"},"custom_provider_config":{"base_provider_type":"openai"}}]}, {"keys":[...]}, {"virtual_keys":[...]}, {"rules":[{"id":"r1","name":"n","cel_expression":"c","targets":[...],"fallbacks":[...]}]} round-trip into the Go types
func TestClient_Errors(t *testing.T)         // 404 → errors.Is(err, ErrNotFound); 409 {"error":"x","message":"Provider up-a already exists"} → *APIError{Status:409, Message:"Provider up-a already exists"}; ctx cancelled → errors.Is(err, context.Canceled)
func TestClient_Health(t *testing.T)         // GET /health 200 → nil; 503 → error
func TestIsStandardProvider(t *testing.T)    // "openrouter","openai","typesafe" true; "up-a" false
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test -race ./internal/engine/bifrost/...`
Expected: FAIL, `undefined: New`.

- [ ] **Step 3: Implement the client** — one private `do(ctx, method, path string, in, out any) error` handles
  Basic auth, JSON, status mapping; path segments escaped with `url.PathEscape`. Routing rule `Target` maps to a
  one-element `targets` array; on decode, take `targets[0]`.

- [ ] **Step 4: Run to verify it passes**

Run: `go test -race ./internal/engine/bifrost/...`
Expected: `ok  github.com/47vigen/remora/internal/engine/bifrost`

- [ ] **Step 5: Commit**

```bash
git add internal/engine/bifrost
git commit -m "feat(engine): front-door management client for providers, keys, virtual keys and routing rules"
```

---

### Task 2: Store — schema, sealed secrets, repositories

**Files:**
- Create: `internal/store/migrations/0002_inference_core.sql`
- Create: `internal/store/secret.go` (Sealer, LoadMasterKey)
- Create: `internal/store/tx.go` (Write, Tx)
- Create: `internal/store/providers.go`, `internal/store/clientkeys.go`, `internal/store/aliases.go`, `internal/store/enginesync.go`
- Test: `internal/store/secret_test.go`, `internal/store/inference_test.go`

**Interfaces:**
- Consumes: `store.Open(ctx, path)` and the embedded migration runner (plan 01); `alias.Alias`, `alias.Kind`,
  `alias.Member` (shared contract — Task 3 creates the package; this task creates `internal/alias/alias.go` with
  just those types if Task 3 has not run yet).
- Produces (consumed by Tasks 4, 6, 8, 9 and plan 05):

```go
// schema (0002_inference_core.sql)
// providers(name TEXT PK, kind TEXT NOT NULL CHECK(kind IN ('builtin','openai-compatible','typesafe-compatible','soniox')), type TEXT NOT NULL DEFAULT '', base_url TEXT NOT NULL DEFAULT '',
//           allow_private_network INTEGER NOT NULL DEFAULT 0, created_at, updated_at)
// provider_keys(id TEXT PK, provider TEXT NOT NULL REFERENCES providers(name) ON DELETE CASCADE, name TEXT NOT NULL,
//               secret BLOB NOT NULL, enabled INTEGER NOT NULL DEFAULT 1, UNIQUE(provider, name))
// client_keys(id TEXT PK, name TEXT NOT NULL UNIQUE, key_mac BLOB NOT NULL UNIQUE, allow TEXT NOT NULL DEFAULT '[]',
//             enabled INTEGER NOT NULL DEFAULT 1, vk_id TEXT NOT NULL DEFAULT '', vk_secret BLOB, created_at, updated_at)
// aliases(name TEXT PK, kind TEXT NOT NULL CHECK(kind IN ('chat','embeddings','transcription','decisions')),
//         capabilities TEXT NOT NULL DEFAULT '[]', context_window INTEGER NOT NULL DEFAULT 0,
//         engine_rule_id TEXT NOT NULL DEFAULT '', created_at, updated_at)
// alias_members(alias TEXT NOT NULL REFERENCES aliases(name) ON DELETE CASCADE, position INTEGER NOT NULL, ref TEXT NOT NULL,
//               PRIMARY KEY(alias, position))
// engine_sync(object TEXT PK, mac TEXT NOT NULL)   // "provider:<name>" | "key:<id>" | "vk:<client_key_id>" | "rule:<alias>"

package store

var ErrNotFound = errors.New("store: not found")
var ErrDuplicate = errors.New("store: duplicate")
var ErrNoSealer = errors.New("store: no sealer configured")

type Sealer struct{ /* aead cipher.AEAD; mac []byte */ }
func LoadMasterKey(dataDir, env string) ([]byte, error) // env (base64, 32 bytes) wins; else read or create dataDir/master.key 0600
func NewSealer(master []byte) (*Sealer, error)
func (s *Sealer) Seal(plaintext, aad string) ([]byte, error)
func (s *Sealer) Open(sealed []byte, aad string) (string, error)
func (s *Sealer) MAC(data []byte) []byte
func (s *Store) SetSealer(sl *Sealer)

type ProviderKind string // "builtin" | "openai-compatible" | "typesafe-compatible" | "soniox" (README "One provider model"; soniox is rendered by plan 04, until then reconcile rejects it with ErrInvalid)
type Provider struct{ Name string; Kind ProviderKind; Type string /* builtin only: the front door's provider, e.g. "openrouter" */; BaseURL string; AllowPrivateNetwork bool }
type ProviderKey struct{ ID, Provider, Name, Secret string; Enabled bool } // Secret in clear in memory only
type ClientKey struct{ ID, Name string; Allow []string; Enabled bool; VKID, VKValue string }

func (s *Store) Write(ctx context.Context, fn func(tx *Tx) error) error // BEGIN IMMEDIATE; commit iff fn returns nil
func (s *Store) ClientKeyByValue(ctx context.Context, value string) (ClientKey, error) // hot path; ErrNotFound
func (s *Store) ProviderKeysByKind(ctx context.Context, kind ProviderKind) ([]string, error) // plaintext secrets of enabled keys of every provider of that kind (plan 04's sweeper)
func (s *Store) Aliases(ctx context.Context) (map[string]alias.Alias, error)
// Engine id mappings for log attribution (plan 05): alias name → routing rule id, client key id → virtual key id.
func (s *Store) AliasRuleIDs(ctx context.Context) (map[string]string, error)
func (s *Store) ClientKeyVKIDs(ctx context.Context) (map[string]string, error)

func (tx *Tx) Providers(ctx context.Context) ([]Provider, error)
func (tx *Tx) PutProvider(ctx context.Context, p Provider) error
func (tx *Tx) DeleteProvider(ctx context.Context, name string) error
func (tx *Tx) ProviderKeys(ctx context.Context) ([]ProviderKey, error)
func (tx *Tx) PutProviderKey(ctx context.Context, k ProviderKey) error // ID generated (uuid v4) when empty
func (tx *Tx) DeleteProviderKey(ctx context.Context, id string) error
func (tx *Tx) ClientKeys(ctx context.Context) ([]ClientKey, error)
// value is caller-chosen (the importer keeps imported key values verbatim; GenerateKeyValue for new keys); only its MAC
// is stored. value "" on an existing key keeps the stored MAC; ID generated when empty.
func (tx *Tx) PutClientKey(ctx context.Context, k ClientKey, value string) (ClientKey, error)
func (tx *Tx) SetClientKeyVK(ctx context.Context, id, vkID, vkValue string) error
func (tx *Tx) DeleteClientKey(ctx context.Context, id string) error
func (tx *Tx) Aliases(ctx context.Context) (map[string]alias.Alias, error)
func (tx *Tx) PutAlias(ctx context.Context, a alias.Alias) error // replaces members in order
func (tx *Tx) DeleteAlias(ctx context.Context, name string) error
func (tx *Tx) SetAliasRuleID(ctx context.Context, name, ruleID string) error
func (tx *Tx) EngineMACs(ctx context.Context) (map[string]string, error)
func (tx *Tx) SetEngineMAC(ctx context.Context, object, mac string) error
func (tx *Tx) DeleteEngineMAC(ctx context.Context, object string) error
func GenerateKeyValue() string // "sk-" + base64url(32 random bytes), no padding
```

- [ ] **Step 1: Write the failing tests**

```go
func TestSealer(t *testing.T) {
    sl := mustSealer(t, bytes.Repeat([]byte{1}, 32))
    ct, _ := sl.Seal("test-upstream-key", "provider_key:k1")
    got, err := sl.Open(ct, "provider_key:k1");          // got == "test-upstream-key", err == nil
    _, err = sl.Open(ct, "provider_key:k2");             // err != nil (AAD bound to the row)
    _, err = mustSealer(t, bytes.Repeat([]byte{2}, 32)).Open(ct, "provider_key:k1") // err != nil (wrong master key)
    ct2, _ := sl.Seal("test-upstream-key", "provider_key:k1"); // !bytes.Equal(ct, ct2) (random nonce)
    // sl.MAC([]byte("a")) is stable across calls and differs between master keys
}
func TestLoadMasterKey(t *testing.T) {
    // env "" → creates dir/master.key, mode 0600, 32 bytes; second call returns the same bytes
    // env = base64 of 32 bytes → returns them and creates no file
    // env = base64 of 16 bytes → error mentioning REMORA_MASTER_KEY
}
func TestClientKeys(t *testing.T) {
    // PutClientKey(name "full", value "sk-e2e-full", allow ["*"]) → ClientKeyByValue("sk-e2e-full") returns it with Allow ["*"], Enabled true
    // raw SELECT of client_keys row contains no "sk-e2e-full" bytes in any column
    // ClientKeyByValue("nope") → ErrNotFound
    // a second key with the same value → errors.Is(err, ErrDuplicate)
    // PutClientKey(existing with Enabled false, value "") → ClientKeyByValue still finds it, Enabled false
    // SetClientKeyVK(id, "vk1", "sk-bf-x") → ClientKeyByValue returns VKID "vk1", VKValue "sk-bf-x"; raw row has no "sk-bf-x"
    // Store.ClientKeyVKIDs() == {id: "vk1"}
    // caller-chosen value "omni-imported-123" (short, no sk- prefix) → stored, ClientKeyByValue finds it, raw row has no such bytes
}
func TestProvidersAndKeys(t *testing.T) {
    // PutProvider{up-a, openai-compatible, base}, PutProviderKey{Provider up-a, Name main, Secret "test-upstream-key"} → ProviderKeys returns Secret in clear, ID non-empty
    // raw provider_keys.secret does not contain "test-upstream-key"
    // DeleteProvider("up-a") cascades its keys
    // PutProvider with Kind "weird" → error (CHECK constraint)
    // ProviderKeysByKind("openai-compatible") == ["test-upstream-key"]; PutProvider{openrouter, builtin, Type "openrouter"} round-trips Type; a disabled key is left out; ProviderKeysByKind("soniox") == [] (not nil)
}
func TestAliases(t *testing.T) {
    // PutAlias{Name "memory/fast", Kind chat, Members [inner, up-c/m1], Capabilities ["structured_outputs"], ContextWindow 131072}
    // Store.Aliases()["memory/fast"] equals it, members in order; PutAlias again with reversed members → reversed
    // DeleteAlias removes members
    // SetAliasRuleID("fast", "r1") → Store.AliasRuleIDs() == {"fast": "r1"}; PutAlias("fast") again keeps "r1"
}
func TestWrite_RollsBack(t *testing.T) // fn writes a provider then returns errors.New("boom") → provider absent, error wraps "boom"
func TestWrite_NoSealer(t *testing.T)  // a store without SetSealer: PutProviderKey → errors.Is(err, ErrNoSealer)
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test -race ./internal/store/...`
Expected: FAIL, `undefined: NewSealer`.

- [ ] **Step 3: Implement** the migration, `Sealer` (HKDF infos `remora/secrets/v1` and `remora/keyhash/v1`,
  nonce prepended to ciphertext), `Write` with `BEGIN IMMEDIATE` and a 30 s busy timeout, and the repositories
  (list columns `allow` and `capabilities` are JSON arrays).

- [ ] **Step 4: Run to verify it passes**

Run: `go test -race ./internal/store/...`
Expected: `ok  github.com/47vigen/remora/internal/store`

- [ ] **Step 5: Commit**

```bash
git add internal/store internal/alias/alias.go
git commit -m "feat(store): providers, client keys and aliases with secrets sealed at rest"
```

---

### Task 3: Alias compilation

**Files:**
- Create/Modify: `internal/alias/alias.go` (shared types from the contract), `internal/alias/compile.go`, `internal/alias/name.go`
- Test: `internal/alias/compile_test.go`, `internal/alias/name_test.go`

**Interfaces:**
- Produces (consumed by Tasks 4, 8 and plans 03, 05, 06):

```go
const DefaultMaxDepth = 5
var ErrCycle = errors.New("alias: cycle")
var ErrDepth = errors.New("alias: nesting too deep")
var ErrUnknownMember = errors.New("alias: unknown member")
var ErrEmpty = errors.New("alias: no members")
func Compile(all map[string]Alias, name string, maxDepth int) ([]Target, error) // maxDepth <= 0 means DefaultMaxDepth
func ValidName(name string) error // ^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$
```

A member ref resolves as an alias when `all` has that key, otherwise as `<provider>/<model>` split at the first `/`;
anything else is `ErrUnknownMember`. Depth counts aliases on the path: the named alias is depth 1. Errors wrap the
sentinel and name the path (`fast -> inner -> fast`).

- [ ] **Step 1: Write the failing table test**

```go
func TestCompile(t *testing.T) {
    a := func(name string, refs ...string) Alias { /* Kind chat, Members from refs */ }
    cases := []struct {
        name  string
        all   []Alias
        root  string
        depth int
        want  []Target
        err   error
    }{
        {"flat", []Alias{a("fast", "up-a/m1", "up-b/m1")}, "fast", 0, []Target{{"up-a", "m1"}, {"up-b", "m1"}}, nil},
        {"nested in order", []Alias{a("memory/fast", "inner", "up-c/m1"), a("inner", "up-a/m1", "up-b/m1")}, "memory/fast", 0,
            []Target{{"up-a", "m1"}, {"up-b", "m1"}, {"up-c", "m1"}}, nil},
        {"dedupe keeps first", []Alias{a("x", "up-a/m1", "y", "up-b/m1"), a("y", "up-b/m1", "up-a/m1")}, "x", 0,
            []Target{{"up-a", "m1"}, {"up-b", "m1"}}, nil},
        {"model with slashes", []Alias{a("emb", "openrouter/qwen/qwen3-embedding")}, "emb", 0,
            []Target{{"openrouter", "qwen/qwen3-embedding"}}, nil},
        {"self cycle", []Alias{a("x", "x")}, "x", 0, nil, ErrCycle},
        {"long cycle", []Alias{a("x", "y"), a("y", "z"), a("z", "x")}, "x", 0, nil, ErrCycle},
        {"diamond is not a cycle", []Alias{a("x", "y", "z"), a("y", "w"), a("z", "w"), a("w", "up-a/m1")}, "x", 0,
            []Target{{"up-a", "m1"}}, nil},
        {"depth 5 ok", chain(5), "c1", 0, []Target{{"up-a", "m1"}}, nil},   // c1 -> c2 -> ... -> c5 -> up-a/m1
        {"depth 6 rejected", chain(6), "c1", 0, nil, ErrDepth},
        {"custom depth", chain(3), "c1", 2, nil, ErrDepth},
        {"unknown member", []Alias{a("x", "nope")}, "x", 0, nil, ErrUnknownMember},
        {"unknown root", nil, "x", 0, nil, ErrUnknownMember},
        {"empty", []Alias{a("x")}, "x", 0, nil, ErrEmpty},
    }
    // got, err := Compile(index(c.all), c.root, c.depth); errors.Is(err, c.err); reflect.DeepEqual(got, c.want)
}
func TestValidName(t *testing.T) // ok: "fast", "memory/fast", "gpt-6-luna", "a.b:c"; bad: "", "/x", "x y", "é", 129 chars
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test -race ./internal/alias/...`
Expected: FAIL, `undefined: Compile`.

- [ ] **Step 3: Implement** depth-first expansion with an on-path set (cycle) and a seen-target set (dedupe).

- [ ] **Step 4: Run to verify it passes**

Run: `go test -race ./internal/alias/...`
Expected: `ok  github.com/47vigen/remora/internal/alias`

- [ ] **Step 5: Commit**

```bash
git add internal/alias
git commit -m "feat(alias): compile nested aliases into a flat, deduplicated chain"
```

---

### Task 4: Reconciliation

**Files:**
- Create: `internal/reconcile/reconcile.go` (Engine, Reconciler, Sync, Change, Run)
- Create: `internal/reconcile/desired.go` (build and validate the desired engine state from a `*store.Tx`)
- Test: `internal/reconcile/reconcile_test.go` (in-memory fake `Engine`)

**Interfaces:**
- Consumes: Task 1 types and method set; Task 2 `store.Write`, `Tx` methods, `Sealer.MAC`; Task 3 `Compile`, `ValidName`.
- Produces (consumed by Task 9 and plans 03, 04, 05):

```go
type Engine interface { // satisfied by *bifrost.Client
    ListProviders(ctx context.Context) ([]bifrost.Provider, error)
    CreateProvider(ctx context.Context, p bifrost.Provider) error
    UpdateProvider(ctx context.Context, p bifrost.Provider) error
    DeleteProvider(ctx context.Context, name string) error
    ListKeys(ctx context.Context, provider string) ([]bifrost.Key, error)
    CreateKey(ctx context.Context, provider string, k bifrost.Key) error
    UpdateKey(ctx context.Context, provider string, k bifrost.Key) error
    DeleteKey(ctx context.Context, provider, id string) error
    ListVirtualKeys(ctx context.Context) ([]bifrost.VirtualKey, error)
    CreateVirtualKey(ctx context.Context, v bifrost.VirtualKey) (bifrost.VirtualKey, error)
    UpdateVirtualKey(ctx context.Context, v bifrost.VirtualKey) error
    DeleteVirtualKey(ctx context.Context, id string) error
    ListRoutingRules(ctx context.Context) ([]bifrost.RoutingRule, error)
    CreateRoutingRule(ctx context.Context, r bifrost.RoutingRule) (bifrost.RoutingRule, error)
    UpdateRoutingRule(ctx context.Context, r bifrost.RoutingRule) error
    DeleteRoutingRule(ctx context.Context, id string) error
}
type InternalProvider struct{ Provider bifrost.Provider; Keys []bifrost.Key } // plan 03 appends the OAuth engine
type Reconciler struct {
    Store    *store.Store
    Engine   Engine
    Internal []InternalProvider // pushed like store providers, never deleted, valid as alias member providers
    InternalRules []bifrost.RoutingRule // pushed by name like alias rules, never deleted (plan 03: one "remora-oauth-<name>" per OAuth provider)
    Reserved []string           // user-facing OAuth provider names (plan 03: antigravity, codex, grok, …): no store provider may take one; valid as alias member providers
    Interval time.Duration      // 0 = 60s
    MaxDepth int                // 0 = alias.DefaultMaxDepth
    Log      *slog.Logger
}
var ErrInvalid = errors.New("reconcile: invalid configuration")
func (r *Reconciler) Sync(ctx context.Context) error // one pass inside store.Write
func (r *Reconciler) Change(ctx context.Context, fn func(ctx context.Context, tx *store.Tx) error) error
func (r *Reconciler) Run(ctx context.Context)        // Sync now, then every Interval; logs errors, returns on ctx.Done
func RuleName(alias string) string                   // "remora:alias:<alias>"
func VirtualKeyName(clientKeyID string) string       // "remora:key:<id>"
func RuleCEL(alias string) string                    // model == <strconv.Quote(alias)> && provider in ["", "openai", "typesafe"]
```

Engine objects remora owns without a store row are known and never deleted: the provider `bifrost.OAuthProvider`
(`remora-oauth`), and any
routing rule named `remora-oauth` or starting with `remora-oauth-`, even before plan 03 fills `Internal` and
`InternalRules`.

Validation (`ErrInvalid`, wraps the cause): no store provider is named `remora-oauth`, starts with `remora-`, or is
in `Reserved`; a `builtin` provider's `type` passes `bifrost.IsStandardProvider` and its name equals its `type`
(the front door keys a standard provider by name; shortcut: one provider per builtin type in 0.1, lift it when a
second account of one builtin is needed); an `openai-compatible`/`typesafe-compatible` provider has no `type`, its
name does not pass `IsStandardProvider`, and it has a base URL; alias names pass `alias.ValidName` and their
prefix before `/` is not a provider name or a `Reserved` name; every alias compiles; every target's provider is a
store, internal or `Reserved` provider. In `Sync`, an alias that fails to compile is logged and its rule removed, so one bad alias never blocks
the rest; in `Change` it fails the change.

Pass order: create/update providers → keys → virtual keys (store the returned `ID`/`Value` with
`SetClientKeyVK`) → rules (store the returned `ID` with `SetAliasRuleID`; a rule found by name with a different id
is re-recorded); then delete unknown rules → virtual keys → keys → providers. An object is pushed when
it is missing in the engine or its MAC (over its desired JSON, secret included) differs from `engine_sync`.

`Change` runs `fn` and the pass in one `store.Write`; when the pass fails the transaction rolls back, and `Change`
then runs a recovery `Sync` so the engine returns to the committed state, and returns the first error. The pass
records every engine object it mutated before the failure; the recovery `Sync` force-repushes those objects
(deleting the ones that did not exist in the committed state) regardless of `engine_sync`, because the rolled-back
MAC still matches the committed desired state while the engine holds the newer write.

- [ ] **Step 1: Write the failing tests** (fake engine records every mutating call and can be told to fail one)

```go
func TestSync_CreatesEverything(t *testing.T) {
    // store: provider up-a (openai), key main; provider up-b; client key c1 allow ["*"]; alias fast [up-a/m1, up-b/m1]
    // after Sync: engine has providers up-a, up-b; key main under up-a; VK "remora:key:c1" Active; rule
    // {Name "remora:alias:fast", CEL RuleCEL("fast"), Target {up-a m1}, Fallbacks [{up-b m1}]}
    // store ClientKeyByValue(c1) has VKID and VKValue set from the engine's answer; Store.AliasRuleIDs()["fast"] == the fake's rule id
}
func TestSync_Idempotent(t *testing.T)          // second Sync makes zero mutating calls
func TestSync_StoreWins(t *testing.T)           // engine has provider "stray", VK "manual", rule "hand-made" → all deleted; Internal provider "internal-x" kept and its key pushed
func TestSync_KeepsOAuthObjects(t *testing.T)   // engine has provider "remora-oauth" and rules "remora-oauth-codex", "remora-oauth-grok", none in the store → after Sync all three still exist (zero Delete calls for them); InternalRules {"remora-oauth-x"} is created and survives a second Sync
func TestSync_EngineWiped(t *testing.T)         // after a Sync, reset the fake engine → next Sync recreates all, stores the new VK value and the new rule id
func TestSync_AliasChange(t *testing.T)         // members reversed → one UpdateRoutingRule with Target {up-b m1}, Fallbacks [{up-a m1}]
func TestSync_NestedAliasFlattened(t *testing.T) // memory/fast [inner, up-c/m1], inner [up-a/m1] → rule targets up-a/m1, fallbacks [up-c/m1]
func TestSync_BadAliasSkipped(t *testing.T)     // alias "x" [nope] and alias fast valid → fast's rule exists, no rule for x, Sync returns nil, error logged
func TestSync_DisabledKeyDeactivatesVK(t *testing.T) // client key Enabled false → UpdateVirtualKey Active false
func TestChange_RecoveryRepushesTouched(t *testing.T) {
    // pass updates provider up-a in the engine, then fails on rule create; tx rolls back (engine_sync MAC back to
    // the old value); recovery Sync must PUT up-a again with the committed JSON even though MAC matches
}

func TestChange_RollsBackOnEngineError(t *testing.T) {
    // fake fails CreateRoutingRule; Change adds alias "slow" → returns error; store has no alias "slow";
    // engine has no rule "remora:alias:slow" and no other object changed
}
func TestChange_Invalid(t *testing.T) {
    // table: openai-compatible provider named "openai" → ErrInvalid; builtin type "up-z" → ErrInvalid; builtin name "or" type "openrouter" → ErrInvalid;
    // alias "up-a/x" while provider up-a exists → ErrInvalid; cycle → ErrInvalid (wraps alias.ErrCycle);
    // member "nobody/m1" → ErrInvalid; store provider "remora-oauth" → ErrInvalid; with Reserved ["codex","grok"]:
    // store provider "codex" → ErrInvalid, alias "grok/fast" → ErrInvalid, alias member "codex/gpt-x" → accepted.
    // In every case except the last: zero engine calls, store unchanged.
}
func TestRuleCEL(t *testing.T) // RuleCEL(`memory/fast`) == `model == "memory/fast" && provider in ["", "openai", "typesafe"]`
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test -race ./internal/reconcile/...`
Expected: FAIL, `undefined: Reconciler`.

- [ ] **Step 3: Implement** `desired.go` (pure: tx → desired objects + MACs, plus validation) and `reconcile.go`.

- [ ] **Step 4: Run to verify it passes**

Run: `go test -race ./internal/reconcile/...`
Expected: `ok  github.com/47vigen/remora/internal/reconcile`

- [ ] **Step 5: Commit**

```bash
git add internal/reconcile
git commit -m "feat(reconcile): push providers, keys, virtual keys and alias rules from the store to the front door"
```

---

### Task 5: Model extraction and the allowlist

**Files:**
- Create: `internal/proxy/extract.go`, `internal/proxy/allow.go`
- Test: `internal/proxy/extract_test.go`, `internal/proxy/allow_test.go`

**Interfaces:**
- Produces (shared contract signatures, plus the sentinels):

```go
const MaxBodyBytes = 100 << 20 // the front door's own cap
var ErrNoModel = errors.New("proxy: no model in request")
var ErrClientFallbacks = errors.New("proxy: fallbacks field is not supported")
var ErrBodyTooLarge = errors.New("proxy: request body too large")
func ExtractModel(r *http.Request) (model string, body io.ReadCloser, err error)
func Allowed(patterns []string, model string) bool
```

`ExtractModel` reads the body up to `MaxBodyBytes`+1 (shortcut: the request body is held in memory; upgrade to a
spooled temp file if transcription uploads approach the cap), finds `model` in a `multipart/form-data` part named
`model` or in the top-level JSON object, rejects a top-level JSON `fallbacks` key, and returns a reader over the
exact bytes it read. Response streams are never buffered; only the request body is.

- [ ] **Step 1: Write the failing tests**

```go
func TestExtractModel(t *testing.T) {
    cases := []struct{ name, contentType, body, want string; err error }{
        {"json", "application/json", `{"model":"fast","messages":[]}`, "fast", nil},
        {"json no content type", "", `{"model":"memory/fast"}`, "memory/fast", nil},
        {"json missing model", "application/json", `{"messages":[]}`, "", ErrNoModel},
        {"json model not string", "application/json", `{"model":7}`, "", ErrNoModel},
        {"fallbacks_field_rejected", "application/json", `{"model":"fast","fallbacks":["openrouter/x"]}`, "", ErrClientFallbacks},
        {"not json", "application/json", `model=fast`, "", ErrNoModel},
    }
    // for each: model, body, err := ExtractModel(req); errors.Is(err, c.err); model == c.want;
    // when err == nil: io.ReadAll(body) is byte-identical to c.body
}
func TestExtractModel_MultipartModelAfterFile(t *testing.T) {
    // multipart: part "file" with 5 MiB of bytes, then field "model" = "soniox/stt-async-v5", then "enable_speaker_diarization"="true"
    // → model "soniox/stt-async-v5"; returned body byte-identical; req Content-Type (with boundary) untouched
}
func TestExtractModel_TooLarge(t *testing.T) // MaxBodyBytes+1 bytes → ErrBodyTooLarge
func TestExtractModel_PersianBytes(t *testing.T) // body with "می‌شود" in a message → returned bytes identical (bytes.Equal)

func TestAllowed(t *testing.T) {
    cases := []struct{ patterns []string; model string; want bool }{
        {[]string{"fast"}, "fast", true},
        {[]string{"fast"}, "fast2", false},
        {[]string{"fast"}, "Fast", false},
        {[]string{"*"}, "memory/fast", true},
        {[]string{"*"}, "openrouter/qwen/qwen3-embedding", true},
        {[]string{"up-a/*"}, "up-a/m1", true},
        {[]string{"openrouter/*"}, "openrouter/qwen/qwen3-embedding", true},
        {[]string{"up-a/*"}, "up-b/m1", false},
        {[]string{"gemini-*"}, "gemini-3.8-flash", true},
        {[]string{"memory/*"}, "memory/fast", true},
        {[]string{"["}, "[", false},          // malformed pattern never matches
        {nil, "fast", false},                 // no entries denies everything
    }
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test -race ./internal/proxy/... -run 'TestExtractModel|TestAllowed'`
Expected: FAIL, `undefined: ExtractModel`.

- [ ] **Step 3: Implement** with `mime/multipart.NewReader` over the held bytes and an `encoding/json` token walk of
  the top level (raw values), so a duplicate or other-case `model` key is seen and refused.

- [ ] **Step 4: Run to verify it passes**

Run: `go test -race ./internal/proxy/... -run 'TestExtractModel|TestAllowed'`
Expected: `ok  github.com/47vigen/remora/internal/proxy`

- [ ] **Step 5: Commit**

```bash
git add internal/proxy/extract.go internal/proxy/allow.go internal/proxy/extract_test.go internal/proxy/allow_test.go
git commit -m "feat(proxy): extract the requested model and check it against the key's allowlist"
```

---

### Task 6: The `/v1` proxy

**Files:**
- Create: `internal/proxy/proxy.go` (Proxy, New, ServeHTTP, routes, auth, upstream request)
- Create: `internal/proxy/response.go` (response header mapping, error mapping, SSE error rewrite, scrub)
- Create: `internal/proxy/errors.go` (remora's own errors in the three shapes)
- Test: `internal/proxy/proxy_test.go` (real store in a temp dir, `httptest.Server` playing the front door)

**Interfaces:**
- Consumes: Task 2 `Store.ClientKeyByValue`, `Store.Aliases`; Task 5.
- Produces (consumed by Tasks 7, 8, 9 and plans 03, 04):

```go
type Proxy struct{ /* upstream *url.URL; store *store.Store; log *slog.Logger; rp *httputil.ReverseProxy */ }
func New(upstream *url.URL, st *store.Store, log *slog.Logger) *Proxy
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) // mounted at /v1/
// Hook for plan 03 (served-by names for internal providers, dropped params). Hooks run in ModifyResponse, after
// remora's own x-remora-* headers are set and before the first byte is written, so they may rewrite those headers.
// reqBody is the exact request body ExtractModel held (nil on the WebSocket route).
type ResponseHook func(r *http.Request, reqBody []byte, resp *http.Response)
func (p *Proxy) OnResponse(h ResponseHook)
```

Route table (remora path → front-door path):

| remora | front door | error shape |
|---|---|---|
| `POST /v1/chat/completions` | `/v1/chat/completions` | OpenAI |
| `POST /v1/responses` | `/v1/responses` | OpenAI |
| `GET /v1/responses` (WebSocket) | Task 7 | OpenAI |
| `POST /v1/messages` | `/anthropic/v1/messages` | Anthropic `{"type":"error","error":{"type","message"}}` |
| `POST /v1/embeddings` | `/v1/embeddings` | OpenAI |
| `POST /v1/audio/transcriptions` | `/v1/audio/transcriptions` | OpenAI |
| `POST /v1/decisions` | `/v1/decisions` | OpenAI |
| `POST /v1/systemone` | `/typesafe/v1/systemone` | TypeSafe `{"detail":{"error_type","message"}}` |
| `GET /v1/models` | Task 8 (remora) | OpenAI |
| anything else | 404 `not_found` | OpenAI |

Request handling, in order: key from `Authorization: Bearer` or `x-api-key` (401 `invalid_api_key` when missing,
unknown or disabled) → `ExtractModel` (400 `model_required`, 400 `fallbacks_not_supported`, 413) → `Allowed`
(403 `model_not_allowed`, message names the model as written) → key has a virtual key (else 503 `key_not_ready`)
→ forward. Upstream headers: delete `Authorization`, `x-api-key`, `Cookie`, `Accept-Encoding`, every `x-bf-*` and
`x-bifrost-*`; set `x-bf-vk: <VKValue>` and `x-bf-passthrough-extra-params: true`. `ReverseProxy` with
`FlushInterval: -1`, no response-header timeout, request context passed through (client disconnect cancels).

Response handling: delete every `x-bifrost-*` and `x-bf-*`; set `x-remora-alias` (when the model is an alias name),
`x-remora-served-by` = `<routing-info-provider>/<routing-info-model>` (when present), `x-remora-fallback` =
`true` iff `x-bifrost-routing-info-is-fallback` is `true`, else `false`. Status ≥ 400: read ≤ 1 MiB; a body with a
top-level `is_bifrost_error` becomes `{"error":{"message","type","code","param"}}` from its `error`; any other body
passes through; in both, `(?i)bifrost|cliproxyapi` → `remora` and `sk-bf-[A-Za-z0-9_-]+` → `[redacted]`.
`text/event-stream`: the body is wrapped by a line reader that forwards every line as it arrives and rewrites only a
`data:` line containing `"is_bifrost_error"` (same mapping, OpenAI shape).

- [ ] **Step 1: Write the failing tests**

```go
func TestProxy_Auth(t *testing.T) {
    // table: no key → 401 {"error":{"code":"invalid_api_key"}}; "Bearer wrong" → 401; disabled key → 401;
    // "x-api-key: sk-e2e-full" → 200; "Bearer sk-e2e-full" → 200. Upstream hit count only for the 200 cases.
}
func TestProxy_Routes(t *testing.T) {
    // table over the route table: remora path → upstream saw path; /v1/messages → /anthropic/v1/messages;
    // /v1/systemone → /typesafe/v1/systemone; GET /v1/nope → 404 OpenAI shape, no upstream hit
}
func TestProxy_Allowlist(t *testing.T) {
    // key allow ["fast","up-a/*"]: "fast" 200, "up-a/m1" 200, "memory/fast" 403 code model_not_allowed,
    // "up-b/m1" 403; on /v1/messages the 403 is {"type":"error","error":{"type":"permission_error",...}};
    // on /v1/systemone it is {"detail":{"error_type":"permission_error",...}}; upstream never hit on 403
}
func TestProxy_StripsClientEngineHeaders(t *testing.T) {
    // client sends Authorization, x-bf-vk: sk-bf-evil, x-bf-compat: true, X-Bifrost-Anything: 1, Cookie: a=b
    // upstream sees x-bf-vk == the key's VKValue, x-bf-passthrough-extra-params == "true",
    // and no Authorization, Cookie, x-bf-compat, x-bifrost-anything
}
func TestProxy_BodyUnchanged(t *testing.T) // upstream receives a byte-identical body (Persian ZWNJ JSON; multipart)
func TestProxy_StreamsUnbuffered(t *testing.T) {
    // upstream writes "data: {chunk1}\n\n", flushes, blocks until the test reads chunk1 from the client side,
    // then writes chunk2 and "data: [DONE]\n\n". Test reads chunk1 before releasing upstream (proves per-chunk flush);
    // concatenated client bytes == upstream bytes
}
func TestProxy_ResponseHeaders(t *testing.T) {
    // upstream sets x-bifrost-routing-info-provider: up-b, -model: m1, -is-fallback: true, x-bifrost-request-id: r
    // client sees x-remora-alias: fast, x-remora-served-by: up-b/m1, x-remora-fallback: true, no header with "bifrost"
    // provider-model request "up-a/m1" → no x-remora-alias
}
func TestProxy_ErrorMapping(t *testing.T) {
    // upstream 429 {"is_bifrost_error":true,"status_code":429,"error":{"type":"rate_limit","code":"insufficient_quota",
    //   "message":"bifrost: all fallbacks failed, key sk-bf-abc"},"extra_fields":{}}
    // → client 429 body exactly {"error":{"message":"remora: all fallbacks failed, key [redacted]","type":"rate_limit","code":"insufficient_quota","param":null}}
    // upstream 400 {"type":"error","error":{"type":"invalid_request_error","message":"bad"}} on /v1/messages → passed through unchanged
}
func TestProxy_SSEErrorEvent(t *testing.T) // stream: data chunk, then `data: {"is_bifrost_error":true,"error":{"message":"Bifrost x"}}` → second event is {"error":{"message":"remora x",...}}, first event byte-identical
func TestProxy_ClientCancelReachesUpstream(t *testing.T) // client cancels mid-stream → upstream handler observes r.Context().Done()
func TestProxy_OnResponseHook(t *testing.T) // hook sees the byte-identical request body and the served-by header; a header it sets reaches the client
func TestProxy_NoResponseTimeout(t *testing.T) // POST /v1/audio/transcriptions, upstream answers after 3 s → 200; the proxy's transport has ResponseHeaderTimeout == 0 and the /v1 server WriteTimeout == 0, so no limit shorter than plan 04's adapter deadline + 60 s applies
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test -race ./internal/proxy/... -run TestProxy_`
Expected: FAIL, `undefined: New`.

- [ ] **Step 3: Implement** `proxy.go`, `response.go`, `errors.go` (`ReverseProxy.Rewrite`, `ModifyResponse`,
  `ErrorHandler` → 502 `upstream_unavailable` in the route's shape).

- [ ] **Step 4: Run to verify it passes**

Run: `go test -race ./internal/proxy/...`
Expected: `ok  github.com/47vigen/remora/internal/proxy`

- [ ] **Step 5: Commit**

```bash
git add internal/proxy
git commit -m "feat(proxy): /v1 reverse proxy with key auth, internal key swap, streaming and engine-free errors"
```

---

### Task 7: Responses over WebSocket

**Files:**
- Create: `internal/proxy/ws.go`
- Modify: `internal/proxy/proxy.go` (route `GET /v1/responses` with `Upgrade: websocket` here)
- Test: `internal/proxy/ws_test.go`

**Interfaces:**
- Consumes: Task 6 auth and header rules; `github.com/coder/websocket` (add with `go get github.com/coder/websocket@latest`).
- Produces: `func (p *Proxy) serveResponsesWS(w http.ResponseWriter, r *http.Request, key store.ClientKey)`

The model arrives per message, not per request: the engine reads `model` from each `response.create` event
(`handlers/wsresponses.go:186-198`). remora accepts the client socket, dials the front door's `/v1/responses` with
the Task 6 upstream headers, and relays both directions frame by frame. Each client text frame whose JSON `type` is
`response.create` is checked with `Allowed` on its top-level `model`; a denied frame is not forwarded and the client
gets `{"type":"error","error":{"type":"invalid_request_error","code":"model_not_allowed","message":...}}`. Server
frames are relayed unchanged except the same `is_bifrost_error` mapping and scrub as Task 6.

- [ ] **Step 1: Write the failing tests** (upstream is an `httptest` WebSocket echo that records frames and headers)

```go
func TestWS_RelaysAndSwapsKey(t *testing.T)   // upgrade with Bearer key; upstream sees x-bf-vk == VKValue, no Authorization; a response.create for "fast" is relayed and the echoed frame reaches the client byte-identical
func TestWS_DeniesModel(t *testing.T)         // key allow ["fast"]; response.create model "up-b/m1" → client gets the model_not_allowed error event; upstream recorded 0 frames; the socket stays open and a later "fast" frame is relayed
func TestWS_RejectsBadKey(t *testing.T)       // no key → HTTP 401 before upgrade
func TestWS_NonCreateFramesPass(t *testing.T) // a frame of another type passes untouched
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test -race ./internal/proxy/... -run TestWS_`
Expected: FAIL, `undefined: serveResponsesWS` (or 404 on upgrade).

- [ ] **Step 3: Implement** two goroutines, one per direction, tied to one context; close both sides with the
  peer's close code.

- [ ] **Step 4: Run to verify it passes**

Run: `go test -race ./internal/proxy/... -run TestWS_`
Expected: `ok  github.com/47vigen/remora/internal/proxy`

- [ ] **Step 5: Commit**

```bash
git add internal/proxy/ws.go internal/proxy/ws_test.go internal/proxy/proxy.go go.mod go.sum
git commit -m "feat(proxy): relay the Responses WebSocket with a per-message allowlist check"
```

---

### Task 8: `GET /v1/models`

**Files:**
- Create: `internal/proxy/models.go`
- Test: `internal/proxy/models_test.go`

**Interfaces:**
- Consumes: Task 2 `Store.Aliases`; Task 5 `Allowed`; Task 6 auth.
- Produces: `func (p *Proxy) serveModels(w http.ResponseWriter, r *http.Request, key store.ClientKey)`

Body: `{"object":"list","data":[{"id":<alias>,"object":"model","created":0,"owned_by":"remora","context_length":<n>,"supported_parameters":[...]}]}`,
sorted by `id`; `context_length` omitted when 0; `supported_parameters` is the alias's `Capabilities` (an empty
array, never null). Only aliases the key may use appear; provider models never appear, even when allowed.

- [ ] **Step 1: Write the failing test**

```go
func TestModels(t *testing.T) {
    // aliases: fast (caps ["structured_outputs","tools"], ctx 131072), memory/fast (caps nil, ctx 0), emb (embeddings)
    // key allow ["*"] → ids ["emb","fast","memory/fast"]; fast.context_length == 131072;
    //   fast.supported_parameters == ["structured_outputs","tools"]; memory/fast has no "context_length" key and "supported_parameters": []
    // key allow ["fast","up-a/*"] → ids ["fast"] only
    // key allow ["up-a/*"] → data == [] (not null)
    // no key → 401; front door never called
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test -race ./internal/proxy/... -run TestModels`
Expected: FAIL (request proxied or 404).

- [ ] **Step 3: Implement** `serveModels` and route `GET /v1/models` to it in `proxy.go`.

- [ ] **Step 4: Run to verify it passes**

Run: `go test -race ./internal/proxy/... -run TestModels`
Expected: `ok  github.com/47vigen/remora/internal/proxy`

- [ ] **Step 5: Commit**

```bash
git add internal/proxy/models.go internal/proxy/models_test.go internal/proxy/proxy.go
git commit -m "feat(proxy): serve /v1/models from aliases the key may use"
```

---

### Task 9: `remora apply -f` (minimal) and wiring into `serve`

**Files:**
- Create: `internal/config/file.go` (the `remora.yaml` types and `Parse`, next to plan 01's env `Config`), `cmd/remora/apply.go` (command, `applyFile`)
- Modify: `cmd/remora/main.go` (register `apply`), plan 01's `serve` command (sealer, client, reconciler, `/v1` mount)
- Test: `cmd/remora/apply_test.go`

**Interfaces:**
- Consumes: Tasks 1–8; plan 01's `config.Config` (`DataDir`, `MasterKey`, `Ports.Bifrost`), `supervise.Credentials`
  (`BifrostAdminUser`, `BifrostAdminPassword`) and the `serve` mux.
- Produces the one `remora.yaml` format (README "One `remora.yaml` format"). Plan 05 extends it with export, diff,
  `--yes`, the server-side apply and a JSON Schema; it adds no second set of types and keeps every field below:

```go
// internal/config/file.go
type File struct {
    Version   int            `yaml:"version,omitempty"` // 1; 0 is read as 1
    Providers []FileProvider `yaml:"providers"`
    Aliases   []FileAlias    `yaml:"aliases"`
    Keys      []FileKey      `yaml:"keys"`
}
type FileProvider struct {
    Name                string    `yaml:"name"`
    Kind                string    `yaml:"kind"` // builtin | openai-compatible | typesafe-compatible | soniox
    Type                string    `yaml:"type,omitempty"` // builtin only, e.g. openrouter
    BaseURL             string    `yaml:"base_url,omitempty"`
    AllowPrivateNetwork bool      `yaml:"allow_private_network,omitempty"`
    Keys                []FileSecretKey `yaml:"keys"`
}
type FileSecretKey struct{ Name, Value string } // Value "env:NAME" required
type FileAlias struct {
    Name          string   `yaml:"name"`
    Kind          string   `yaml:"kind"`
    Members       []string `yaml:"members"`
    Capabilities  []string `yaml:"capabilities,omitempty"`
    ContextWindow int      `yaml:"context_window,omitempty"`
}
type FileKey struct {
    Name    string   `yaml:"name"`
    Value   string   `yaml:"value,omitempty"` // "env:NAME"; omitted on a new key → generated and printed once
    Allow   []string `yaml:"allow"`
    Enabled *bool    `yaml:"enabled,omitempty"` // default true
}
func Parse(b []byte) (File, error) // yaml.v3 with KnownFields(true): an unknown field is an error naming its path

// cmd/remora/apply.go
func applyFile(ctx context.Context, r *reconcile.Reconciler, f config.File, getenv func(string) string, out io.Writer) error
```

`applyFile` runs one `r.Change`: upsert everything in the file (provider keys matched by provider + name, client
keys by name), delete what the file does not name, then reconcile. A literal secret (not `env:`) or an unset env
variable is an error naming the field, before anything is written.

`serve` after the engines are healthy: `LoadMasterKey(cfg.DataDir, cfg.MasterKey)` → `NewSealer` →
`st.SetSealer` → `bifrost.New("http://127.0.0.1:<cfg.Ports.Bifrost>", creds.BifrostAdminUser, creds.BifrostAdminPassword, &http.Client{})` → `Reconciler{...}` →
`go r.Run(ctx)` → mount `proxy.New(frontDoorURL, st, log)` at `/v1/`. The `/v1` server path has no write timeout.

- [ ] **Step 1: Write the failing tests** (fake `reconcile.Engine` from Task 4's test helpers, exported in
  `internal/reconcile/reconciletest`)

```go
func TestApplyFile(t *testing.T) {
    // file with provider up-a (keys main: env:UP_KEY), alias fast [up-a/m1], key full (env:FULL_KEY, allow ["*"])
    // getenv {"UP_KEY":"test-upstream-key","FULL_KEY":"sk-e2e-full"} → store has all three; ClientKeyByValue("sk-e2e-full") ok; engine has the rule
    // apply again with alias fast removed → alias and its rule gone; provider kept
}
func TestApplyFile_Errors(t *testing.T) {
    // table: key value "sk-literal" → error mentions "keys[0].value" and "env:"; env var unset → error names the variable;
    // unknown kind → error; in each case the store is unchanged
}
func TestApplyFile_GeneratesKey(t *testing.T) // new key without value → out contains one line "full: sk-..." and that value authenticates
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test -race ./cmd/remora/... -run TestApplyFile`
Expected: FAIL, `undefined: applyFile`.

- [ ] **Step 3: Implement** `apply.go` (`go get go.yaml.in/yaml/v3@latest`) and the `serve` wiring.

- [ ] **Step 4: Run to verify it passes, then the whole tree**

Run: `go test -race ./...`
Expected: every package `ok`.

- [ ] **Step 5: Commit**

```bash
git add cmd/remora internal/config internal/reconcile/reconciletest go.mod go.sum
git commit -m "feat(cli): remora apply -f and the inference core wired into serve"
```

---

### Task 10: Fakes — default upstream answers, scripted streams, a test-side client

Plan 01 ships the fakes: one binary on port `9090`, one upstream per path prefix `/u/{upstream}`, scripts per
`(upstream, route)` through `PUT /_control/upstreams/{upstream}/routes/{route}`, recordings through
`GET /_control/upstreams/{upstream}/requests`, `POST /_control/reset`, and `Step.SSE` reserved for this plan. This
task fills in what plan 01 left at `501` and adds a thin client; it changes none of plan 01's shapes.

**Files:**
- Modify: `test/fakes/` (route defaults for `openai.chat` stream and non-stream, `openai.embeddings`,
  `openai.decisions`, `typesafe.systemone`, `models`; `Step.SSE` streaming)
- Create: `test/e2e/fakesctl/fakesctl.go` (test-side client of plan 01's control API)
- Test: `test/fakes/control/defaults_test.go`

**Interfaces:**
- Consumes: plan 01 `control.Script`, `control.Step`, `control.RecordedRequest`, the route constants, and the e2e
  helpers `fakesURL` and `resetFakes`.
- Produces (consumed by Task 11 and plans 03, 04):

```go
package fakesctl
// Script sets the script of one (upstream, route) through PUT /_control/upstreams/{upstream}/routes/{route}.
func Script(t testing.TB, base, upstream, route string, s control.Script)
// Requests returns GET /_control/upstreams/{upstream}/requests.
func Requests(t testing.TB, base, upstream string) []control.RecordedRequest
```

`Step.SSE`: when set, the reply is `text/event-stream`, each entry is sent as `data: <entry>\n\n` and flushed, then
`data: [DONE]\n\n`. "Once, then the default" is a one-step script with `Repeat: false`; "the next two" is two steps.
Several remora providers point at one fake with different base URLs (`http://fakes:9090/u/a`,
`http://fakes:9090/u/b`).

- [ ] **Step 1: Write the failing test**

```go
func TestDefaultsAndSSE(t *testing.T) {
    // unscripted POST /u/a/v1/chat/completions → 200 OpenAI chat body; stream:true → SSE ending with [DONE]
    // unscripted /v1/embeddings, /v1/decisions, /v1/systemone, GET /v1/models → 200 in each API's shape
    // script a/openai.chat {steps:[{status:429, body:`{"error":{"code":"insufficient_quota"}}`}]} → 429 once, then 200
    // script with SSE entries → each arrives as its own flushed event, then [DONE]; recordings hold the exact bytes
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test -race ./test/fakes/... -run TestDefaultsAndSSE`
Expected: FAIL (`501` from the unscripted route).

- [ ] **Step 3: Implement** the route defaults, `Step.SSE`, and `fakesctl`.

- [ ] **Step 4: Run to verify it passes**

Run: `go test -race ./test/fakes/...`
Expected: `ok`

- [ ] **Step 5: Commit**

```bash
git add test/fakes test/e2e/fakesctl
git commit -m "test(fakes): default upstream answers, SSE scripts and a test-side control client"
```

---

### Task 11: End-to-end suite for the inference core

**Files:**
- Create: `test/e2e/testdata/remora.yaml`
- Modify: `test/e2e/compose.yaml` (remora env `E2E_UPSTREAM_KEY`, `E2E_KEY_FULL`, `E2E_KEY_LIMITED`, `E2E_KEY_OFF`;
  mount `./testdata` at `/e2e:ro`)
- Modify: plan 01's `TestMain` in `test/e2e` (after the stack is healthy: `docker compose exec -T remora remora apply -f /e2e/remora.yaml`)
- Create: `test/e2e/client_test.go` (`do` helper with the leak guard), `test/e2e/chat_test.go`,
  `test/e2e/fallback_test.go`, `test/e2e/keys_test.go`, `test/e2e/embeddings_test.go`, `test/e2e/decisions_test.go`

**Interfaces:**
- Consumes: Task 9 `apply`, Task 10 `fakesctl`, plan 01's e2e helpers (`remoraURL`, `fakesURL`, `execIn`,
  `resetFakes`, `waitHealthy`).

`test/e2e/testdata/remora.yaml`:

```yaml
providers:
  - {name: up-a, kind: openai-compatible, base_url: http://fakes:9090/u/a, allow_private_network: true, keys: [{name: main, value: env:E2E_UPSTREAM_KEY}]}
  - {name: up-b, kind: openai-compatible, base_url: http://fakes:9090/u/b, allow_private_network: true, keys: [{name: main, value: env:E2E_UPSTREAM_KEY}]}
  - {name: up-c, kind: openai-compatible, base_url: http://fakes:9090/u/c, allow_private_network: true, keys: [{name: main, value: env:E2E_UPSTREAM_KEY}]}
  - {name: ts-a, kind: typesafe-compatible, base_url: http://fakes:9090/u/ts-a, allow_private_network: true, keys: [{name: main, value: env:E2E_UPSTREAM_KEY}]}
  - {name: ts-b, kind: typesafe-compatible, base_url: http://fakes:9090/u/ts-b, allow_private_network: true, keys: [{name: main, value: env:E2E_UPSTREAM_KEY}]}
aliases:
  - {name: fast, kind: chat, members: [up-a/m1, up-b/m1], capabilities: [structured_outputs, tools], context_window: 131072}
  - {name: inner, kind: chat, members: [up-a/m1]}
  - {name: memory/fast, kind: chat, members: [inner, up-c/m1]}
  - {name: emb, kind: embeddings, members: [up-a/emb-1, up-b/emb-1]}
  - {name: jev, kind: decisions, members: [ts-a/jev, ts-b/jev]}
keys:
  - {name: full, value: env:E2E_KEY_FULL, allow: ["*"]}
  - {name: limited, value: env:E2E_KEY_LIMITED, allow: [fast, "up-a/*"]}
  - {name: off, value: env:E2E_KEY_OFF, allow: ["*"], enabled: false}
```

The `do` helper fails the test when any response header name or value, or any body byte sequence, matches
`(?i)bifrost|cliproxy|sk-bf-` — every test below runs through it, so the whole suite is the leak guard.

- [ ] **Step 1: Write the tests** (each calls plan 01's `resetFakes` first)

```go
const persian = "می‌شود و نمی‌خواهم"

func TestChatStreamPersian(t *testing.T) {
    // script up-a SSE: three chunks id "chatcmpl-1" whose delta contents concatenate to persian, a final chunk with
    //   usage {prompt_tokens:3, completion_tokens:5, completion_tokens_details:{reasoning_tokens:2}}
    // POST /v1/chat/completions {model:"fast", stream:true, stream_options:{include_usage:true}} with key full
    // assert: 200, content-type text/event-stream; concatenated delta.content bytes == []byte(persian) (bytes.Equal);
    //   every chunk's "id" == "chatcmpl-1"; the last JSON chunk has usage.completion_tokens_details.reasoning_tokens == 2;
    //   last event is "data: [DONE]"; x-remora-alias "fast"; x-remora-served-by "up-a/m1"; x-remora-fallback "false"
}
func TestChatNonStreamPersian(t *testing.T)   // choices[0].message.content bytes == persian; "model" equals the fake's model
func TestJSONSchemaPassthrough(t *testing.T) {
    // request response_format {type:"json_schema", json_schema:{name:"r", strict:true, schema:{...}}} to "fast"
    // captured up-a body's response_format is JSON-equal to the sent one
}
func TestToolCallsInStream(t *testing.T) {
    // script SSE with tool_calls deltas: index 0, id "call_1", function name "lookup", arguments split over 3 chunks `{"q":` `"می‌شود"` `}`
    // client assembles → one call "lookup" with arguments `{"q":"می‌شود"}` byte-equal; finish_reason "tool_calls"
}
func TestFallbackWithinRequest(t *testing.T) {
    // table × {stream false, stream true}:
    //   429 {"error":{"type":"rate_limit_exceeded"}}, 429 {"error":{"code":"insufficient_quota"}}, 402 {"error":{"message":"out of credit"}}
    // script up-a/openai.chat one step {Status, Body}; up-b default; POST "fast" → 200, x-remora-served-by "up-b/m1",
    //   x-remora-fallback "true"; Requests(a) == 1 and Requests(b) == 1
}
func TestFallbackAllFail(t *testing.T) // up-a and up-b both 429 → client 429 with {"error":{...}} OpenAI shape
func TestNestedAlias(t *testing.T) {
    // script up-a/openai.chat one 429 step; POST "memory/fast" → served-by "up-c/m1", alias header "memory/fast"; Requests(b) == 0 (inner has only up-a)
}
func TestAllowlist(t *testing.T) {
    // key limited: "fast" 200; "up-a/m1" 200; "memory/fast" 403 {"error":{"code":"model_not_allowed"}}; "up-b/m1" 403;
    // no upstream request recorded for the 403s; key off → 401; no key → 401
}
func TestModelsList(t *testing.T) {
    // key full → ids ["emb","fast","inner","jev","memory/fast"]; fast.context_length 131072,
    //   fast.supported_parameters ["structured_outputs","tools"]; no id contains "up-" or "ts-"
    // key limited → ids ["fast"]
}
func TestEmbeddings(t *testing.T) {
    // script up-a body with data[0].embedding "AAAAAA==", data[1].embedding "AAAAAB==" (base64 strings)
    // POST /v1/embeddings {model:"emb", input:["a","b"], dimensions:1536, encoding_format:"base64"}
    // captured up-a body: dimensions == 1536, encoding_format == "base64", input == ["a","b"]
    // response data[i].embedding are those exact strings; x-remora-alias "emb"
}
func TestDecisions(t *testing.T) {
    // script ts-a 429 {"detail":{"error_type":"insufficient_quota","message":"free allowance used"}} as two steps
    // POST /v1/systemone {model:"jev", state:"...", questions:{...}} → 200 native System One body from ts-b; served-by "ts-b/jev"
    // POST /v1/decisions {model:"jev", input:"...", questions:[{type:"predicate",...}]} → 200; served-by "ts-b/jev"; fallback "true"
}
func TestUnknownParamReachesUpstream(t *testing.T) // chat with "remora_e2e_extra": {"a":1} → captured up-a body has it, JSON-equal
func TestReconcileAfterEngineRestart(t *testing.T) // kill the front-door process inside the container (plan 01's supervisor restarts it); after health is back, "fast" still answers 200 within 30 s
```

- [ ] **Step 2: Run the suite**

Run: `go test -tags e2e -race ./test/e2e/... -run 'TestChat|TestJSONSchema|TestToolCalls|TestFallback|TestNested|TestAllowlist|TestModelsList|TestEmbeddings|TestDecisions|TestUnknownParam|TestReconcile'`
Expected: `ok  github.com/47vigen/remora/test/e2e`. If `TestFallbackWithinRequest/stream` reports
`x-remora-fallback: false` while up-b served it, the engine's stream header snapshot predates the fallback: record
it in `docs/patches.md` as an upstream issue, and have the proxy fall back to the body's `extra_fields` only for that
header (do not weaken the test).

- [ ] **Step 3: Fix what fails** in the owning task's package, rerun Step 2 until green.

- [ ] **Step 4: Commit**

```bash
git add test/e2e
git commit -m "test(e2e): streaming fidelity, fallback, nested aliases, allowlists, models, embeddings and decisions"
```
