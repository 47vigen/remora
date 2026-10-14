# Driving the engines: management API map

Date: 2026-10-14. Paths: `B` = `bifrost/transports/bifrost-http`, `C` = `CLIProxyAPI`. Read, not run.
Purpose: remora hides both engines, so every user operation must map to an engine API call.

## Bifrost

| area | endpoints | runtime? |
|---|---|---|
| providers and their keys | `/api/providers[/{p}[/keys[/{id}]]]`, `refresh-models` (`B/handlers/providers.go:219-233`) | yes (`ReloadProvider`) |
| model catalog, pricing | `/api/models*`, `PUT /api/models/catalog`, `/api/pricing/force-sync`, pricing overrides | yes |
| virtual keys | `/api/governance/virtual-keys` CRUD, rotate, budget override (`governance.go:1806-1814`); allowlist in `provider_configs[].allowed_models`, deny by default | yes (`ReloadVirtualKey`) |
| routing rules and fallbacks | `/api/routing/rules[/{id}]` (`routing.go:119-123`): CEL expression, weighted targets, `fallbacks[]`, `chain_rule`, priority | yes |
| logs | `GET /api/logs` with filters `virtual_key_ids`, `models`, `providers`, `status`, time range, `aliases`, `routing_rule_ids`, cost/latency/token ranges, `content_search`, paging (`logging.go:632-764`) | |
| analytics | `/api/logs/stats`, `/histogram/*`, `/rankings/by-dimension` (dimension `virtual_keys`) | |
| live logs | WebSocket `GET /ws` (`websocket.go:93`) | |
| config | `GET/PUT /api/config`; `restart_required` only for Prometheus labels, CORS, pool size, logging toggle, max body size (`config.go:608-741`) | mostly |
| health | `GET /health` | |
| spec | `docs/openapi/openapi.{yaml,json}`, about 298 `/api` paths; no Go client | |

Constraints:

- The UI is embedded and always served (`B/main.go:73`, `server.go:3626`); no flag disables it.
- One listener for inference and management; cannot split ports.
- Admin auth: `admin_username`/`admin_password`, Basic or session bearer.
- A config store (SQLite/Postgres) is required for governance, routing and config handlers.
- Log store: SQLite, Postgres or ClickHouse; `retention_days` default 365.

## CLIProxyAPI (management v8, `C/internal/api/server_management_v8.go:14-62`)

| area | endpoints | runtime? |
|---|---|---|
| config | `GET/PUT/PATCH /config`, `/config.yaml`, `/config/*path` | yes (watcher hot reload) |
| credentials | `GET/POST/DELETE /credentials`, `GET /credentials/models`, `PATCH /credentials/status`, `PATCH /credentials/fields`, `POST /credentials/refresh`; list returns status, message, disabled, unavailable, `next_retry_after`, quota, success/failed, recent requests, cooldowns | yes |
| cooldown and quota | `POST /routing/cooldown/reset`, `/plugins/:id/quota`, v0 `/quota/fetch`, `/quota/reset` | yes |
| OAuth | `GET /oauth/auth-url?provider=` (URL + `state`), `GET /oauth/status?state=` (`wait`/`ok`/`error`), `DELETE /oauth/session`, `GET/POST /oauth/callback` (`code`+`state` or full `redirect_url`) | yes |
| usage | `/observability/usage/api-keys` (in memory); `/observability/usage/queue?count=N` pops records (destructive) | |

OAuth providers in this version: claude, codex, antigravity, kimi, kimi-ai, xai (Grok), devin, meta. No Gemini CLI
OAuth (Gemini is API-key only).

Constraints:

- `remote-management.disable-control-panel: true` removes the panel.
- Management routes exist only with a secret (`remote-management.secret-key` or `MANAGEMENT_PASSWORD`); loopback
  only unless `allow-remote`; five failures ban for 30 minutes.

### OAuth redirect URIs

| provider | redirect |
|---|---|
| codex | `http://localhost:1455/auth/callback` |
| antigravity | localhost port 51121 |
| claude | `http://localhost:54545/callback` |
| xai (Grok) | device flow; the verification URL is returned and the server polls |

A browser on another machine lands on a dead localhost page after login. The supported remote flow is: show the
auth URL, let the user paste the final URL back, `POST /oauth/callback {redirect_url}`, poll `/oauth/status`.

## remora operation map

| remora operation | engine calls |
|---|---|
| add an API-key provider | Bifrost `POST /api/providers` + `/keys` |
| add an OAuth account | CLIProxyAPI `oauth/auth-url` → paste → `POST oauth/callback` → poll `oauth/status` |
| disable or remove an account | CLIProxyAPI `PATCH /credentials/status`, `DELETE /credentials` |
| account quota and health | CLIProxyAPI `GET /credentials`, `/credentials/models`, quota, cooldown reset |
| create a client key | Bifrost `POST /api/governance/virtual-keys` |
| define an alias | compile to Bifrost `POST /api/routing/rules` (flat targets + fallbacks) |
| usage and logs | Bifrost `/api/logs`, `/api/logs/stats`, rankings, `/ws` |
| global settings | Bifrost `PUT /api/config` |
