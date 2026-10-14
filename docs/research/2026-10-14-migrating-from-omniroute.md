# Migrating from OmniRoute

Date: 2026-10-14. Sources: OmniRoute source (`diegosouzapw/OmniRoute`), a live OmniRoute 3.8.51 database (read-only,
no secret values printed), Bifrost and CLIProxyAPI source. This is the basis for `remora import omniroute`.

## Client keys

- OmniRoute `api_keys.key` holds plaintext keys: 35 characters, prefix `sk-`. `key_hash` is `sha256(key)` (verified
  on 14 of 14 rows) and is only a lookup index.
- Bifrost virtual keys must start with `sk-bf-` (`plugins/governance/main.go:33`) and are matched by that prefix.
  Only config.json lets the caller choose a value. remora issues its own keys anyway (see the spec), so imported
  keys keep their old body behind remora's prefix and consumers change URL and key.

## OAuth accounts

- OmniRoute encrypts `access_token`, `refresh_token`, `id_token` at rest: `enc:v1:<iv>:<ct>:<tag>`, AES-256-GCM with
  a key derived from `STORAGE_ENCRYPTION_KEY` (static salt, fallback to an older salt;
  `src/lib/db/encryption.ts`). The key lives in the data directory's `server.env`.
- OAuth client IDs are identical on both sides, so refresh tokens remain valid after a move:
  Codex `app_EMoamEEZ73f0CkXaXp7hrann` (`CLIProxyAPI/internal/auth/codex/openai_auth.go:27`); Antigravity
  `1071006060591-…` (`CLIProxyAPI/internal/auth/antigravity/constants.go:6`), both the `ide` and `cli` profiles.
- Antigravity auth file (CLIProxyAPI `sdk/auth/antigravity.go:309-343`): `type`, `access_token`, `refresh_token`,
  `expires_in`, `timestamp`, `expired` (RFC3339), `email`, `project_id`. OmniRoute holds all of them;
  `project_id` is plaintext and matches `provider_specific_data.projectId`.
- Codex auth file (`internal/auth/codex/token.go:227-247`): `type`, `id_token`, `access_token`, `refresh_token`,
  `account_id`, `email`, `expired`, `last_refresh`, `plan_type`. `account_id` comes from the id_token's
  `chatgpt_account_id` claim; CLIProxyAPI also refills it on refresh.

## Running both at once

- Codex refresh tokens rotate on every refresh (single use). Whichever system refreshes first invalidates the
  other's copy. Move a Codex account in one step and stop the old system's refresh first.
- Google normally does not rotate Antigravity refresh tokens, so sharing would survive, but moving accounts one at
  a time keeps quota and ban attribution clean.

## Behaviour to verify on the new side

- Embedding vectors: same model, same width, cosine about 1 against OmniRoute's output, `encoding_format: base64`
  included.
- Streaming with Persian text: half-spaces intact, one id per stream, usage in the final chunk.
