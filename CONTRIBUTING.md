# Contributing to remora

Thank you for helping. This guide covers what you need to build remora, the checks every change must pass, and
the rules that keep the repository healthy.

## Prerequisites

- Go 1.27 or newer.
- Docker with Buildx.
- A clone with its submodules: `git clone --recurse-submodules https://github.com/47vigen/remora`.

## The gates

Every change passes two gates before review:

1. `make lint test`: lint and the unit tests with the race detector.
2. `make image fakes-image e2e`: build the remora image and the fakes image, then run the end-to-end suite
   against them.

## What CI runs

A pull request runs only the jobs its changed files can break; `scripts/ci-changes.sh` decides, and the
`changes` job in `.github/workflows/ci.yml` calls it. The `ci` job at the end is the one required check.

| You change | Jobs that run |
| --- | --- |
| `CHANGELOG.md` or the version manifest only | none |
| `README.md` or `docs/` only | `docs` (`go test ./test/docs`) |
| Go code, `go.mod`, `go.sum`, `api/openapi.yaml` | `lint`, `test` (race detector, OpenAPI drift) |
| `web/` or `api/openapi.yaml` | `web` |
| `engines/`, `internal/engine/`, `internal/quota/`, `docs/patches.md` | `engine` (builds the OAuth engine, contract tests, pin check) |
| shipped code: `cmd/`, `internal/` (not `_test.go`), `web/`, `engines/`, `api/`, `deploy/`, `Dockerfile`, `go.mod`, `test/fakes/`, `test/e2e/` | `image`, then `e2e`, `console` and `quickstart` |
| the `README.md` quick start block | `image`, then `quickstart` and the rest of that chain |
| `.github/` | `actionlint` |
| `.github/workflows/ci.yml` or `scripts/ci-changes.sh` | everything |

A push to `main`, the nightly run (02:17 UTC) and a manual run (Actions, `ci`, Run workflow) run everything.
`govulncheck` is a separate workflow: on a pull request it runs only when Go code or `go.mod` changes, and it also
runs weekly.

The e2e job writes its 15 slowest tests to the job summary.

## Commits and pull requests

- Use [Conventional Commits](https://www.conventionalcommits.org/) with scopes, for example `feat(proxy):`,
  `fix(store):`, `test(e2e):` or `ci:`.
- Every change reaches `main` through a pull request.

## Engines

The engines live as git submodules under `engines/`. They are never edited in place. An engine patch goes
upstream first; see `docs/patches.md` for how a pending patch is carried until it ships.

## Tests

No real provider keys in tests, ever. Tests talk to the fakes in `test/fakes`.
