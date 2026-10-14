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

## Commits and pull requests

- Use [Conventional Commits](https://www.conventionalcommits.org/) with scopes, for example `feat(proxy):`,
  `fix(store):`, `test(e2e):` or `ci:`.
- Every change reaches `main` through a pull request.

## Engines

The engines live as git submodules under `engines/`. They are never edited in place. An engine patch goes
upstream first; see `docs/patches.md` for how a pending patch is carried until it ships.

## Tests

No real provider keys in tests, ever. Tests talk to the fakes in `test/fakes`.
