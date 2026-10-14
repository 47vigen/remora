# Guide for coding agents

This is the single guide for coding agents working in this repository. Read it before changing anything.

## Layout

```
remora/
  cmd/remora/                 main: serve, apply, export, import, version
  internal/
    api/                      generated server interfaces + handlers
    auth/                     admin session, API token, client keys
    proxy/                    /v1 proxy, allowlist, model extraction, headers
    alias/                    definitions, nesting, compilation, cooling
    engine/bifrost/           client for the engine's management API
    engine/cliproxyapi/       client for the engine's management API
    supervise/                child processes, health, restart, versions
    soniox/                   the speech-to-text adapter
    store/                    SQLite schema, migrations, repositories
    importer/omniroute/       remora import omniroute
    reconcile/                store → engines
  api/openapi.yaml            the management API contract
  web/                        the UI (Bun, Vite)
  engines/bifrost             submodule
  engines/cliproxyapi         submodule
  test/e2e/                   end-to-end tests against the built image
  test/fakes/                 fake upstreams: OpenAI-compatible, Soniox, TypeSafe
  deploy/                     compose example
  docs/specs/  docs/plans/  docs/research/  docs/patches.md  docs/parity.md
  Dockerfile  LICENSE  NOTICE  THIRD_PARTY_NOTICES.md
  README.md  CONTRIBUTING.md  SECURITY.md  CODE_OF_CONDUCT.md  CHANGELOG.md
  AGENTS.md  CLAUDE.md
  .github/  (workflows, issue and PR templates, CODEOWNERS)
```

## Gates

Run both before you call a change done:

1. `make lint test`: the code passes golangci-lint, and every unit test passes with the race detector.
2. `make image fakes-image e2e`: the remora image and the fakes image build from source, and the end-to-end suite
   passes against them, proving the shipped image works as a whole.

## Rules

- English only in files.
- Conventional Commits with scopes; one branch and one pull request per plan.
- Never edit `engines/`. Move an engine only through `engines/versions.env` plus the submodule pin.
- No owner infrastructure, host names, keys or product names anywhere, including fixtures.
- Tests before code. e2e tests carry the `e2e` build tag and talk to the fakes only.
- Engine names never appear in a user-facing string, except in `/api/system/version` and the credits.

## Where things go

- Implementation plans: `docs/plans/`.
- Research: `docs/research/`.
