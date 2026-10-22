# Maintaining remora

One-time repository settings and the routine of releases and engine updates. None of this is automated: a
maintainer with admin rights on the repository does it by hand.

## Branch protection (a ruleset on `main`)

- Require a pull request before merging.
- Required status check: `ci`, the last job of the `ci` workflow. It is green when every job that ran passed;
  a job whose inputs did not change is skipped, and `ci` ignores skipped jobs. The per-job names (`lint`, `test`,
  `engine`, `web`, `image`, `e2e`, `console`, `quickstart`, `actionlint`) need not be required as well. Do not
  require `govulncheck`: it is a separate workflow, and on a pull request it runs only when Go code or `go.mod`
  changes.
- Squash merge only; require linear history.
- Block force pushes and deletion.
- No required approving review: a single maintainer cannot approve their own pull request, and a required review
  would block the bot's auto-merge.
- Enable "Allow auto-merge" in the repository settings (General, Pull Requests).

## The bot App

Pull requests and tags created with `GITHUB_TOKEN` start no workflows: CI would never run on a release or engine
bump pull request, and a release-please tag would never start `release.yml`. A GitHub App token fixes both.

- Create a GitHub App owned by the repository owner. Repository permissions: Contents read and write, Pull requests
  read and write. No webhook.
- Install it on this repository only.
- Generate a private key for it.
- Store the App's Client ID (from its settings page, not the numeric App ID) as the repository secret
  `REMORA_BOT_CLIENT_ID` and the private key as `REMORA_BOT_PRIVATE_KEY`.

`release-please.yml` and `engine-bump.yml` use these secrets. Without them, both fail at their first step; a
release can still be started by hand with the `release.yml` "Run workflow" button and a `tag` input.

## First publish

GHCR creates new packages private. After the first release:

- Open the `remora` package on GHCR (the owner's Packages tab).
- Link it to this repository (Package settings, "Connect repository").
- Set its visibility to public.

## Releasing

- `release-please` keeps a release pull request open on `main` with the next version and its changelog. Commits of
  type `feat`, `fix`, `perf`, `deps` and `revert` are releasable; `feat` bumps the minor version before 1.0.
- Merge the release pull request. release-please tags `vX.Y.Z` and creates the GitHub release; the tag starts
  `release.yml`, which builds amd64 and arm64 natively, runs the quick start and the e2e suite on both, and only
  then writes the `X.Y.Z`, `X.Y` and `latest` tags, signs the image and adds engine versions and SBOMs to the
  release notes.
- A prerelease tag (`vX.Y.Z-rc.N`) never moves `X.Y` or `latest`.
- A red `release.yml` leaves a GitHub release without an image, and no image tag moves. Either re-run the failed
  jobs (Actions, the failed run, "Re-run failed jobs"), or fix forward and release the next patch.

## Engines

- `engine-bump.yml` runs daily. For each engine it asks `tools/enginebump` whether the submodule is behind the
  latest upstream release of the same major. If it is, it opens (or updates) one pull request on branch
  `engine-bump/<engine>` titled `deps(engines): bump <engine> to <tag>` with auto-merge on. It merges only when every
  required check, including `e2e` and `quickstart`, is green. A merged bump cuts a patch release whose notes list
  the new engine versions.
- An engine on a fork (a pending patch listed in [`docs/patches.md`](patches.md)) is never bumped or rebased
  automatically. Each day the job checks the patch's upstream pull request; once it is merged and contained in the
  latest upstream release of the same major, the job opens a pull request that points the submodule back at
  upstream at that tag and removes the row from `docs/patches.md`. Until then it writes one line to the job summary.
  To get a newer engine before the patch merges, rebase the fork by hand.
- A new upstream major (for example the front-door engine's `transports/v3` or the OAuth engine's `v9`) is only
  reported in the job summary. Each major so far has changed module paths and management APIs, so it needs a
  hand-made pull request.
- To dry-run the decision locally: `go run ./tools/enginebump -engine <name> -dry-run`.

## Verifying an image

Every released image index is signed with cosign keyless signing from `release.yml`:

```sh
cosign verify ghcr.io/47vigen/remora@<digest> \
  --certificate-identity-regexp '^https://github\.com/47vigen/remora/\.github/workflows/release\.yml@refs/' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

Each GitHub release lists the digest and has the SPDX SBOM of both platforms attached.
