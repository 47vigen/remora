# Release and parity Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship remora 0.1: automated, signed, multi-arch releases; engines that follow upstream through green pull
requests; dependency and vulnerability hygiene; a public `docs/parity.md` whose every row is proven; and a README
quick start that CI runs verbatim.

**Architecture:** release-please (driven by a GitHub App token, so its pull requests and tags trigger workflows)
writes the changelog and tags `vX.Y.Z`. The tag starts `release.yml`: each architecture is built natively on its
own runner and pushed by digest with SBOM and provenance attestations; both digests then pass the README quick
start and the full e2e suite; only then are the semver and `latest` tags written, the index signed with cosign
keyless, and the GitHub release annotated with the engine versions from `versions.json`. A small Go tool decides
engine bumps; a scheduled workflow turns its decision into an auto-merging pull request gated by CI. Parity and
README rules are Go tests, so `go test ./...` fails when a row has no proof.

**Tech Stack:** GitHub Actions, release-please, Docker Buildx (native amd64 and arm64 runners), cosign (Sigstore
keyless), Dependabot, govulncheck, Go 1.27 (`golang.org/x/mod/semver`), bash, jq, gh.

**Spec:** [`docs/specs/2026-10-14-remora-0.1-design.md`](../specs/2026-10-14-remora-0.1-design.md). Parity source:
[`docs/research/2026-10-14-reference-deployment.md`](../research/2026-10-14-reference-deployment.md) (MUST and MUST
NOT lists). Shared contracts: [`docs/plans/README.md`](README.md).

## Global Constraints

- Repository `github.com/47vigen/remora`, public from the first commit, Apache-2.0.
- Image `ghcr.io/47vigen/remora`, "for amd64 and arm64 with an SBOM, signed with `cosign`, and a GitHub release".
- "`release-please` writes the changelog and tags semver."
- "A scheduled workflow moves each engine submodule to its latest release tag and opens a pull request; it merges
  only when the end-to-end suite is green. Each release notes the engine versions inside."
- "`docs/patches.md` lists every engine patch with its upstream pull request; the submodule returns to upstream when
  it merges."
- "Pull requests only to `main`, Conventional Commits with scopes."
- No owner hosts, domains, product names or secrets anywhere in the repository. The parity file is generic:
  capabilities, provider kinds, endpoints, behaviours.
- A stranger never reads the words Bifrost or CLIProxyAPI outside the credits (spec, success criterion 2).
- No real provider and no real key in any CI job, ever (shared contracts, Testing).
- Runtime env vars exactly as in the shared contracts: `REMORA_LISTEN`, `REMORA_DATA`, `REMORA_ADMIN_PASSWORD`,
  `REMORA_LOG_LEVEL`, `REMORA_ENGINE_DIR`, `REMORA_MASTER_KEY`, `REMORA_PORT_ROUTER`, `REMORA_PORT_ACCOUNTS`,
  `REMORA_PORT_SPEECH`.
- Every workflow sets `permissions: {}` at the top and grants the minimum per job.
- Third-party actions are referenced by major tag (`@v4`); Dependabot keeps them current.

## Engine facts this plan relies on (verified)

- Bifrost needs cgo for `mattn/go-sqlite3` (`transports/Dockerfile:27-28`, `-tags sqlite_static` at `:43-48`) and
  sets its version with `-X main.Version` (`transports/bifrost-http/main.go:76`). `transports/go.mod` declares
  `go 1.27.0`.
- CLIProxyAPI's plugin host is cgo-only (`internal/pluginhost/loader_unix.go:1`, `//go:build cgo && ...`); version
  via `-X main.Version` (`cmd/server/main.go:47`); module `github.com/router-for-me/CLIProxyAPI/v8`, `go 1.26.0`.
- Both upstreams build arm64 natively on `ubuntu-24.04-arm` rather than under QEMU
  (CLIProxyAPI `.github/workflows/docker-image.yml:52`; Bifrost `.github/workflows/release-pipeline.yml:2326`).
- Tags, from `git ls-remote --tags` on 2026-10-14:
  - Bifrost tags every module separately (`core/v*`, `framework/v*`, `plugins/*/v*`, `transports/v*`, ...). The
    gateway release is `transports/vX.Y.Z` (latest `transports/v2.3.0`). Prereleases look like
    `transports/v2.0.0-prerelease3`.
  - CLIProxyAPI tags `vX.Y.Z` (latest `v8.0.28`). Every major so far (v6, v7, v8) has changed the Go module path.
- The Codex translator drops `max_tokens`/`max_completion_tokens` (commented out) and forces
  `parallel_tool_calls: true` (`internal/translator/codex/openai/chat-completions/codex_openai_request.go:63-81`).
- Bifrost's `TranscriptionSegment` has no `speaker` field (`core/schemas/transcriptions.go:213-221`).

## Decisions

- **Native arm64 runners, not QEMU, not cross-compilation.** Two of the three Go builds are cgo (sqlite, the plugin
  host), and the UI stage runs Bun. Under QEMU every compiler process is emulated, which costs a several-fold
  slowdown for the longest stage of the build. Cross-compiling cgo needs an aarch64 C toolchain and target sysroot
  inside plan 01's Dockerfile, a second build path to keep correct. Native `ubuntu-24.04-arm` runners are free for
  public repositories, need no Dockerfile change, and are what both upstreams use. Each architecture is built,
  then tested, on its own runner; a manifest list joins them.
- **One GitHub App token for release-please and engine bumps.** Pull requests and tags created with
  `GITHUB_TOKEN` do not trigger workflows, so CI would never run on a release or bump pull request, and a
  release-please tag would never start `release.yml`. A GitHub App installed on the repository (Contents and Pull
  requests: read and write) fixes both. Its id and key are repository secrets `REMORA_BOT_APP_ID` and
  `REMORA_BOT_PRIVATE_KEY`. Creating the App is a one-time owner action, documented in `docs/maintaining.md`.
- **Publish only after tests.** Per-arch images are pushed by digest only. Tags (`X.Y.Z`, `X.Y`, `latest`) are
  written after the quick start and the e2e suite pass on both digests, so a red release never moves a tag.
  Prerelease tags (`vX.Y.Z-rc.N`) never move `X.Y` or `latest`.
- **Engine on a fork: skip, never rebase.** When `.gitmodules` points an engine at a fork (a pending patch in
  `docs/patches.md`), the bump job does not move it. Rebasing a fork in CI needs write access to the fork and
  resolves conflicts in someone else's code without review, which is the drift the spec forbids. Instead, each day
  the job checks the patch's upstream pull request: once it is merged and contained in the latest upstream release
  of the same major, the job opens a pull request that points the submodule back at upstream at that tag and
  removes the row from `docs/patches.md`. Until then it writes one line to the job summary. A maintainer who wants
  a newer engine before the patch merges rebases the fork by hand.
- **Same major only.** The bump tool proposes tags within the current major. A new major (Bifrost
  `transports/v3`, CLIProxyAPI `v9`) is reported in the job summary and needs a hand-made pull request, because
  each major so far changed module paths and management APIs.
- **Dependabot, not Renovate.** It is built into GitHub (no App to install), feeds the repository's security
  alerts, and covers all four ecosystems remora has: `gomod`, `bun`, `github-actions`, `docker`. Renovate's extra
  power (custom tag patterns for submodules) is exactly what the engine bump tool already owns, so its
  `gitsubmodule` ecosystem stays off.
- **govulncheck, not CodeQL.** Reachability-based, near-zero false positives, one fast job, and it blocks on what
  remora actually calls. CodeQL's Go and TypeScript queries mostly duplicate `golangci-lint` and the UI linters at a
  far higher CI cost.
- **Release commit types.** `feat`, `fix`, `perf`, `deps` and `revert` are releasable and appear in the changelog.
  Engine bumps commit as `deps(engines): ...`, so following upstream cuts a patch release whose notes list the new
  engine versions.

## Review Focus

1. **An annotated upstream tag.** `git ls-remote` returns the tag object's SHA and a peeled `^{}` line; a submodule
   gitlink is the commit SHA. Matching on the tag object SHA would think every engine is untagged and re-bump it
   daily. Pinned by `TestParseLsRemotePeeledWins` and `TestCurrentMatchesPeeledCommit` (Task 4).
2. **A new upstream major or a prerelease.** `transports/v3.0.0`, `v9.0.0`, `transports/v2.4.0-prerelease1` must
   never be proposed as a bump. Pinned by `TestLatest` rows (Task 4) and `TestDecide` "major" case.
3. **A release red on arm64 only.** No tag may move. Pinned by the job graph (`publish` needs both `verify` legs)
   and checked in Task 3 Step 6 with `actionlint` plus a `needs:` assertion.
4. **A parity row whose test was renamed or never written.** The row must turn CI red, not stay "green" by text.
   Pinned by `TestParityProofsResolve` (Task 7), which looks for `func TestName(` in the named directory.
5. **README drift.** An edited quick start that no longer works, or an engine name leaking into the README, must
   fail CI. Pinned by the `quickstart` job running the README's own blocks (Task 2) and
   `TestReadmeNoEngineNamesOutsideCredits` (Task 2).

---

### Task 1: release-please, the bot token, and maintainer documentation

**Files:**
- Create: `release-please-config.json`, `.release-please-manifest.json`
- Create: `.github/workflows/release-please.yml`
- Create: `docs/maintaining.md`
- Modify: `.github/workflows/ci.yml` (plan 01) — add an `actionlint` job if plan 01 has none

**Interfaces:**
- Consumes: nothing; release-please creates `CHANGELOG.md` on its first run.
- Produces: tags `vX.Y.Z` pushed by the App (start `release.yml`, Task 3); secrets `REMORA_BOT_APP_ID`,
  `REMORA_BOT_PRIVATE_KEY` (used by Tasks 1 and 5); required-check names listed in `docs/maintaining.md`.

- [ ] **Step 1: Write `release-please-config.json`**

```json
{
  "$schema": "https://raw.githubusercontent.com/googleapis/release-please/main/schemas/config.json",
  "packages": {
    ".": {
      "release-type": "go",
      "package-name": "remora",
      "include-component-in-tag": false,
      "bump-minor-pre-major": true,
      "changelog-path": "CHANGELOG.md",
      "changelog-sections": [
        {"type": "feat", "section": "Features"},
        {"type": "fix", "section": "Bug Fixes"},
        {"type": "perf", "section": "Performance"},
        {"type": "deps", "section": "Dependencies and engines"},
        {"type": "revert", "section": "Reverts"},
        {"type": "docs", "section": "Documentation", "hidden": true},
        {"type": "ci", "section": "CI", "hidden": true},
        {"type": "test", "section": "Tests", "hidden": true},
        {"type": "chore", "section": "Chores", "hidden": true}
      ]
    }
  }
}
```

`.release-please-manifest.json`: `{".": "0.0.0"}` — the first `feat` then releases `0.1.0`.

- [ ] **Step 2: Validate the config against the published schema**

Run: `pipx run check-jsonschema --schemafile https://raw.githubusercontent.com/googleapis/release-please/main/schemas/config.json release-please-config.json`
Expected: `ok -- validation done`

- [ ] **Step 3: Write `.github/workflows/release-please.yml`**

Trigger `push: branches: [main]`; `permissions: {}`; one job `release-please` on `ubuntu-24.04` with
`permissions: contents: read`. Steps: `actions/create-github-app-token@v2` (`app-id: ${{ secrets.REMORA_BOT_APP_ID }}`,
`private-key: ${{ secrets.REMORA_BOT_PRIVATE_KEY }}`), then `googleapis/release-please-action@v4` with
`token: ${{ steps.app.outputs.token }}`, `config-file: release-please-config.json`,
`manifest-file: .release-please-manifest.json`. No build here: the App-pushed tag starts `release.yml`.

- [ ] **Step 4: Write `docs/maintaining.md`** (documented, not automated)

Sections, each a short list:
- **Branch protection (a ruleset on `main`)**: pull request required; required status checks `lint`, `test` (it
  runs plan 05's OpenAPI drift step), `image`, `e2e` (plan 01), `engine` (plan 03), `web` (plan 06), `quickstart`, `govulncheck`,
  `actionlint`; squash merge only, linear history; no force push, no deletion; **no required approving
  review** (a single maintainer cannot approve their own pull request, and it would block bot auto-merge);
  "Allow auto-merge" enabled in repository settings.
- **The bot App**: create a GitHub App owned by the repository owner, permissions Contents and Pull requests
  read/write, no webhook; install it on this repository only; store `REMORA_BOT_CLIENT_ID` (the App's Client ID) and
  `REMORA_BOT_PRIVATE_KEY` as repository secrets. Why: `GITHUB_TOKEN` events do not start workflows.
- **First publish**: after the first release, open the `remora` package on GHCR, link it to the repository and set
  it public (GHCR creates new packages private).
- **Releasing**: merge the release-please pull request; `release.yml` does the rest. A red `release.yml` leaves a
  GitHub release without an image: fix forward and release the next patch, or re-run the failed jobs.
- **Engines**: how the daily bump works, the fork rule (link to `docs/patches.md`), and that a new major is a
  hand-made pull request.
- **Verifying an image**: the `cosign verify` command from Task 3 Step 3.

- [ ] **Step 5: Lint workflows**

Add (if absent) a job `actionlint` to `ci.yml`: `ubuntu-24.04`, `permissions: contents: read`, checkout, then
`docker run --rm -v "$PWD:/repo" -w /repo rhysd/actionlint:latest -color`.
Run locally: `docker run --rm -v "$PWD:/repo" -w /repo rhysd/actionlint:latest`
Expected: exit 0, no output.

- [ ] **Step 6: Commit**

```bash
git add release-please-config.json .release-please-manifest.json .github/workflows/release-please.yml .github/workflows/ci.yml docs/maintaining.md
git commit -m "ci(release): add release-please with a bot token and document maintenance"
```

---

### Task 2: README, compose example, and the quick-start smoke job

**Files:**
- Modify: `README.md` (full rewrite)
- Create: `docs/README.md` (docs index)
- Create: `deploy/compose.yaml`
- Create: `scripts/quickstart-smoke.sh`
- Create: `test/docs/readme_test.go`
- Modify: `.github/workflows/ci.yml` — add job `quickstart`

**Interfaces:**
- Consumes: plan 01's `image` job and its artifact `images` (a `docker save` of `remora:dev` and
  `remora-fakes:dev`, loaded by its `e2e` job the same way); the fakes from `test/fakes` (plan 01: one port, `9090`,
  one upstream per `/u/<name>` prefix, any key accepted; plan 02 gives the chat route a default answer for any model);
  the management operations in `api/openapi.yaml` (plan 05) that sign in, create a provider and create a key;
  env var literals in the Go code under `cmd/` and `internal/` (plans 01–05).
- Produces: `scripts/quickstart-smoke.sh` — env `REMORA_IMAGE` (required), `FAKES_IMAGE` (required); prints
  `quickstart: OK` and exits 0, or exits non-zero. Used again by `release.yml` (Task 3).

- [ ] **Step 1: Write the failing tests** in `test/docs/readme_test.go` (package `docs_test`, repo root is `../..`)

```go
func TestReadmeNoEngineNamesOutsideCredits(t *testing.T) {
    readme := mustRead(t, "README.md")
    body, _, ok := strings.Cut(readme, "\n## Credits")
    if !ok { t.Fatal("README has no '## Credits' section") }
    for _, name := range []string{"Bifrost", "bifrost", "CLIProxyAPI", "cliproxyapi"} {
        if strings.Contains(body, name) { t.Errorf("engine name %q appears outside ## Credits", name) }
    }
}

func TestReadmeDocumentsEveryEnvVar(t *testing.T) {
    // every "REMORA_[A-Z_]+" string literal in non-test .go files under cmd/ and internal/ appears in README.md
    // (REMORA_PORT_ROUTER, REMORA_PORT_ACCOUNTS and REMORA_PORT_SPEECH by name; no engine-named variable exists).
}

func TestReadmeQuickStartIsMarked(t *testing.T) {
    // README contains exactly one "<!-- quickstart:begin -->" and one "<!-- quickstart:end -->", begin before end,
    // with at least two ```sh blocks between them.
}

func TestDocsIndexLinksResolve(t *testing.T) {
    // every relative markdown link in docs/README.md and README.md points at an existing path.
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./test/docs/ -run 'TestReadme|TestDocsIndex' -v`
Expected: FAIL — `README has no '## Credits' section`, and the quick start markers are missing.

- [ ] **Step 3: Rewrite `README.md`**

Sections, in order (plain English, short sentences):
1. One-paragraph intro (keep today's first lines and the "What it does" list in present tense; drop the
   "Status: design" banner).
2. **Quick start**, between `<!-- quickstart:begin -->` and `<!-- quickstart:end -->`. Shell blocks (```sh) are
   executed by the smoke job in order; prose between them is for people.
   - Block 1: `export REMORA_ADMIN_PASSWORD="$(openssl rand -hex 16)"`, then
     `docker run -d --name remora -p 8080:8080 -v remora-data:/data -e REMORA_ADMIN_PASSWORD ghcr.io/47vigen/remora:latest`,
     then a wait loop on `http://localhost:8080/healthz` (plan 01) of at most 120 s, the same budget as the supervisor's engine start and the image `HEALTHCHECK` start period, so a cold first boot on a slow host is not reported as a failure.
   - Prose: open `http://localhost:8080`, sign in with that password; **Providers → Add** (an OpenRouter key);
     optionally **Providers → a subscription provider → Add account** (sign in, paste the final address back;
     link to the ToS section); **Keys → Create** (the value is shown once).
   - Block 2 ("the same from a shell"): `export UPSTREAM_URL=https://openrouter.ai/api/v1 UPSTREAM_KEY=sk-or-your-key`,
     then `curl` calls for: sign in (keeping the session cookie in a temp file), create a provider named
     `openrouter` with that base URL and key, create a key allowed `openrouter/*` and capture its value with `jq`.
     Use the exact operations from `api/openapi.yaml`.
   - Block 3: `curl -s http://localhost:8080/v1/chat/completions -H "Authorization: Bearer $REMORA_KEY" -H 'Content-Type: application/json' -d '{"model":"openrouter/openai/gpt-4o-mini","messages":[{"role":"user","content":"Say hi"}]}' | jq -r '.choices[0].message.content'`
3. **Configuration**: table of `REMORA_LISTEN`, `REMORA_DATA`, `REMORA_ADMIN_PASSWORD` (used only while no admin
   exists; afterwards the stored password wins), `REMORA_LOG_LEVEL`, `REMORA_ENGINE_DIR`, `REMORA_MASTER_KEY`
   (base64, 32 bytes; when unset, `$REMORA_DATA/master.key` is generated — back it up with the volume, because
   without it stored provider keys cannot be decrypted), `REMORA_PORT_ROUTER`, `REMORA_PORT_ACCOUNTS`,
   `REMORA_PORT_SPEECH` with the defaults from the shared contracts, plus every other
   variable the code reads (for example plan 04's `REMORA_SONIOX_*`; `TestReadmeDocumentsEveryEnvVar` lists any
   that are missing); the `/data` layout;
   a pointer to `deploy/compose.yaml`.
4. **Architecture**: the spec's diagram with the engines named generically ("routing engine", "subscription
   engine"), each marked "see Credits"; one paragraph: one port, one image, engines as loopback child processes.
5. **Subscription accounts and terms of service**: keep today's paragraph.
6. **Dropped parameters**: keep today's Codex paragraph; add that `x-remora-dropped-params` lists them and link
   `docs/parity.md`.
7. **Verifying an image**: the `cosign verify` command (Task 3 Step 3).
8. **Documentation**: link `docs/README.md`.
9. **Credits**: Bifrost by Maxim AI (Apache-2.0) and CLIProxyAPI (MIT), what each does inside remora, link to
   `THIRD_PARTY_NOTICES.md` and `docs/patches.md`.
10. **License**.

`docs/README.md`: one table linking `specs/`, `plans/`, `research/`, `parity.md`, `patches.md`, `maintaining.md`,
and `../api/openapi.yaml`, one line each.

- [ ] **Step 4: Write `deploy/compose.yaml`**

```yaml
services:
  remora:
    image: ghcr.io/47vigen/remora:latest
    ports: ["8080:8080"]
    environment:
      REMORA_ADMIN_PASSWORD: ${REMORA_ADMIN_PASSWORD:?set REMORA_ADMIN_PASSWORD}
    volumes: ["remora-data:/data"]
    restart: unless-stopped
volumes:
  remora-data: {}
```

Run: `REMORA_ADMIN_PASSWORD=x docker compose -f deploy/compose.yaml config -q`
Expected: exit 0.

- [ ] **Step 5: Write `scripts/quickstart-smoke.sh`**

`set -euo pipefail`; a `trap` that removes the `remora` and `qs-fakes` containers and the `remora-data` volume.
Steps: start `$FAKES_IMAGE` as `qs-fakes` publishing the OpenAI-compatible fake's port on the host; extract the
```sh blocks between the quickstart markers (`awk`) into one script; apply exactly these literal substitutions
with `sed`, and fail if any literal is not found (so README drift fails loudly instead of calling a real provider):

| README literal | replaced by |
|---|---|
| `ghcr.io/47vigen/remora:latest` | `$REMORA_IMAGE` |
| `https://openrouter.ai/api/v1` | `http://172.17.0.1:9090/u/quickstart/v1` (the default bridge gateway on Linux runners; plan 01's fakes port and upstream prefix) |
| `sk-or-your-key` | `fake-key` (the fakes accept any key) |
| `openai/gpt-4o-mini` | `m1` (any model gets plan 02's default chat answer) |

Run the result with `bash`; the last block's output must be non-empty; print `quickstart: OK`.

- [ ] **Step 6: Add the `quickstart` job to `ci.yml`**

`needs: image`; `runs-on: ubuntu-24.04` (a fresh runner with only Docker); `permissions: contents: read`;
download the artifact `images` and `docker load -i images.tar`, exactly as plan 01's `e2e` job does; then
`REMORA_IMAGE=remora:dev FAKES_IMAGE=remora-fakes:dev scripts/quickstart-smoke.sh`; then
`REMORA_ADMIN_PASSWORD=x docker compose -f deploy/compose.yaml config -q`.

- [ ] **Step 7: Run tests and the smoke locally**

Run: `go test ./test/docs/ -run 'TestReadme|TestDocsIndex' -v`
Expected: PASS (4 tests).
Run: `make image fakes-image && REMORA_IMAGE=remora:dev FAKES_IMAGE=remora-fakes:dev scripts/quickstart-smoke.sh`
Expected: last line `quickstart: OK`. (On macOS replace the bridge address with `host.docker.internal` via
`QS_UPSTREAM_HOST`, an optional override the script reads.)

- [ ] **Step 8: Commit**

```bash
git add README.md docs/README.md deploy/compose.yaml scripts/quickstart-smoke.sh test/docs/readme_test.go .github/workflows/ci.yml
git commit -m "docs(readme): add a quick start that CI runs, configuration reference and credits"
```

---

### Task 3: the release workflow

**Files:**
- Create: `.github/workflows/release.yml`
- Create: `scripts/release-notes.sh`
- Create: `scripts/testdata/versions.json` (fixture)

**Interfaces:**
- Consumes: tag `vX.Y.Z` (Task 1); `Dockerfile` with build args `REMORA_VERSION`, `REMORA_COMMIT`, `BIFROST_VERSION`
  and `CLIPROXYAPI_VERSION` (plan 01; the engine ones come from `engines/versions.env`); `versions.json` at
  `$REMORA_ENGINE_DIR/versions.json` = `/usr/local/lib/remora/engines/versions.json` inside the image (plan 01),
  a JSON object whose keys are engine names and whose values are a version string or an object with a `version`
  field (plan 01 writes `{"<engine>": {"version", "sha256"}}`); `scripts/quickstart-smoke.sh` (Task 2); the e2e
  harness reading the images under test from `REMORA_IMAGE` and `FAKES_IMAGE` (defaults `remora:dev`,
  `remora-fakes:dev`) and run by `go test -tags e2e ./test/e2e/...` (plan 01); `remora version` printing the version it was built with
  (plan 01).
- Produces: `ghcr.io/47vigen/remora:{X.Y.Z, X.Y, latest}` (multi-arch index, signed, with SBOM and provenance
  attestations per platform); `scripts/release-notes.sh <versions.json> <image-ref> <digest>` → Markdown on
  stdout, starting with the marker line `<!-- remora-release-assets -->`.

- [ ] **Step 1: Write `scripts/testdata/versions.json` and run the not-yet-existing script**

```json
{"remora": "v0.1.0", "bifrost": "transports/v2.3.0", "cliproxyapi": {"version": "v8.0.28"}}
```

Run: `scripts/release-notes.sh scripts/testdata/versions.json ghcr.io/47vigen/remora sha256:abc`
Expected: FAIL (`No such file or directory`).

- [ ] **Step 2: Write `scripts/release-notes.sh`**

`jq` turns the file into a table `| component | version |` (value, or `.version` when it is an object). Then an
"Image" section (`docker pull <image-ref>@<digest>`) and a "Verify" section with the Step 3 command. First line is
the marker.

Run: `scripts/release-notes.sh scripts/testdata/versions.json ghcr.io/47vigen/remora sha256:abc`
Expected: contains `| bifrost | transports/v2.3.0 |`, `| cliproxyapi | v8.0.28 |`,
`ghcr.io/47vigen/remora@sha256:abc`, and the first line is `<!-- remora-release-assets -->`.

- [ ] **Step 3: Write `.github/workflows/release.yml`**

Triggers: `push: tags: ['v*.*.*']`, and `workflow_dispatch` with input `tag` (re-runs and the no-App fallback).
`permissions: {}`; `concurrency: release-${{ inputs.tag || github.ref_name }}`, `cancel-in-progress: false`.
`TAG` = `inputs.tag || github.ref_name`; `IMAGE` = `ghcr.io/47vigen/remora`.

| job | runs on | permissions | does |
|---|---|---|---|
| `build` (matrix `{platform: linux/amd64, runner: ubuntu-24.04}`, `{platform: linux/arm64, runner: ubuntu-24.04-arm}`) | `${{ matrix.runner }}` | `contents: read`, `packages: write` | checkout `ref: TAG`, `submodules: recursive`; `cat engines/versions.env >> "$GITHUB_ENV"`; `docker/setup-buildx-action@v3`; `docker/login-action@v3` to ghcr.io with `GITHUB_TOKEN`; `docker/build-push-action@v6` with `platforms: ${{ matrix.platform }}`, `build-args` `REMORA_VERSION=${TAG}`, `REMORA_COMMIT=${{ github.sha }}`, `BIFROST_VERSION`, `CLIPROXYAPI_VERSION` (from env), `sbom: true`, `provenance: mode=max`, `outputs: type=image,name=${IMAGE},push-by-digest=true,name-canonical=true,push=true`, `cache-from/to: type=gha,scope=${{ matrix.platform }}`; upload the digest as artifact `digest-<arch>` |
| `verify` (same matrix), `needs: build` | `${{ matrix.runner }}` | `contents: read`, `packages: read` | checkout `ref: TAG`; login; `docker pull ${IMAGE}@<digest of this arch>`; assert `docker run --rm <ref> version` output contains `TAG`; `docker build -f test/fakes/Dockerfile -t remora-fakes:dev .`; `REMORA_IMAGE=<ref> FAKES_IMAGE=remora-fakes:dev scripts/quickstart-smoke.sh`; `go test -tags e2e -count=1 -timeout 45m ./test/e2e/...` with `REMORA_IMAGE=<ref>` |
| `publish`, `needs: verify` | `ubuntu-24.04` | `contents: read`, `packages: write`, `id-token: write` | download both digests; `docker/metadata-action@v5` with `images: IMAGE`, tags `type=semver,pattern={{version}},value=TAG`, `type=semver,pattern={{major}}.{{minor}},value=TAG,enable=${{ !contains(TAG, '-') }}`, `type=raw,value=latest,enable=${{ !contains(TAG, '-') }}`, `flavor: latest=false`; job concurrency `release-publish` (one publish at a time across releases); X.Y is enabled only when TAG is the newest stable patch of its minor and `latest` only when TAG is the newest stable tag, so a rerun of an older release never takes a channel back; `docker buildx imagetools create` from both `IMAGE@sha256:<digest>` under a run-scoped staging tag only; read the index digest with `docker buildx imagetools inspect`; `sigstore/cosign-installer@v3`; `cosign sign --yes ${IMAGE}@<index digest>`; then the verify command below; only then `imagetools create` the public tags from the signed digest and check the version tag resolves to it; output `digest` |
| `notes`, `needs: publish` | `ubuntu-24.04` | `contents: write`, `packages: read` | checkout; login; `docker create --platform linux/amd64 IMAGE@digest` and `docker cp <id>:/usr/local/lib/remora/engines/versions.json .`; write `sbom-linux-amd64.spdx.json` and `sbom-linux-arm64.spdx.json` from `docker buildx imagetools inspect IMAGE@digest --format '{{ json (index .SBOM "linux/amd64").SPDX }}'` (and arm64); `gh release view TAG --json body -q .body`, cut everything from the marker line onward (idempotent re-runs), append `scripts/release-notes.sh` output, `gh release edit TAG --notes-file`; `gh release upload TAG sbom-*.spdx.json --clobber` |

Verify command (also used in README and `docs/maintaining.md`):

```sh
cosign verify ghcr.io/47vigen/remora@<digest> \
  --certificate-identity-regexp '^https://github\.com/47vigen/remora/\.github/workflows/release\.yml@refs/' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

- [ ] **Step 4: Lint**

Run: `docker run --rm -v "$PWD:/repo" -w /repo rhysd/actionlint:latest`
Expected: exit 0.

- [ ] **Step 5: Prove the arm64 build and its attestations locally** (Apple silicon builds arm64 natively)

Run: `docker buildx build --platform linux/arm64 --build-arg REMORA_VERSION=v0.0.0-local --build-arg BIFROST_VERSION=transports/v2.3.0 --build-arg CLIPROXYAPI_VERSION=v8.0.28 --sbom=true --provenance=mode=max -o type=oci,dest=/tmp/remora-arm64.tar .`
Expected: build succeeds; `tar -tf /tmp/remora-arm64.tar | grep -c blobs/` is greater than the image's layer
count (the SBOM and provenance blobs are present).

- [ ] **Step 6: Assert the gate in the job graph**

Run: `yq '.jobs.publish.needs, .jobs.notes.needs, .jobs.verify.needs' .github/workflows/release.yml`
Expected: `verify`, `publish`, `build` — nothing tags or signs before both `verify` legs pass.

- [ ] **Step 7: Commit**

```bash
git add .github/workflows/release.yml scripts/release-notes.sh scripts/testdata/versions.json
git commit -m "ci(release): build amd64 and arm64 natively, test, then tag, sign and annotate"
```

---

### Task 4: the engine bump tool

**Files:**
- Create: `tools/enginebump/main.go` (flags, exec of `git` and `gh`, writing outputs)
- Create: `tools/enginebump/decide.go` (pure logic)
- Create: `tools/enginebump/decide_test.go`
- Modify: `go.mod` (`golang.org/x/mod`)

**Interfaces:**
- Consumes: `.gitmodules` with submodules at paths `engines/bifrost` and `engines/cliproxyapi` (plan 01);
  `docs/patches.md` (plan 04 Task 8) as one Markdown table. Columns are found by header name, case-insensitive,
  in any order; required: `engine` (`bifrost` or `cliproxyapi`), `upstream PR` (a full
  `https://github.com/<owner>/<repo>/pull/<n>` URL) and `base tag` (the upstream release tag the fork branch
  starts from, e.g. `transports/v2.3.0`). Plan 04 Task 8 writes exactly these columns
  (`| engine | file | change | why | fork commit | upstream PR | base tag | remove when |`) and states the fork
  rule above the table, so this task does not edit `docs/patches.md`.
- Produces (package `main`, consumed by Task 5's workflow through the CLI):

```go
type Engine struct {
    Name      string // "bifrost" | "cliproxyapi"
    Path      string // "engines/bifrost"
    Upstream  string // "https://github.com/maximhq/bifrost"
    TagPrefix string // "transports/v" | "v"
}
var engines = []Engine{
    {"bifrost", "engines/bifrost", "https://github.com/maximhq/bifrost", "transports/v"},
    {"cliproxyapi", "engines/cliproxyapi", "https://github.com/router-for-me/CLIProxyAPI", "v"},
}

type Tag struct{ Name, SHA string } // SHA is the commit (peeled) SHA

func ParseLsRemote(out string) []Tag
// Latest returns the highest stable semver tag with prefix and the given major; prereleases excluded.
func Latest(tags []Tag, prefix string, major int) (Tag, bool)
func Current(tags []Tag, prefix, gitlink string) (Tag, bool)
func Between(tags []Tag, prefix string, from, to Tag) []Tag // stable tags in (from, to], ascending

type Patch struct{ Engine, PR, BaseTag string }
func ParsePatches(md string) ([]Patch, error)
func WithoutPatch(md, engine string) (string, error) // md with that engine's row removed

type Kind string // "none" | "bump" | "unfork" | "hold" | "major"
type Action struct {
    Kind     Kind
    Engine   Engine
    From, To Tag
    Note     string // one line for the job summary
}
// merged reports whether the patch's PR is merged and its merge commit is an ancestor of tag.
func Decide(ctx context.Context, e Engine, gitmodulesURL, gitlink string, tags []Tag, patch *Patch,
    merged func(ctx context.Context, pr string, tag Tag) (bool, error)) (Action, error)
func PRTitle(a Action) string // "deps(engines): bump bifrost to transports/v2.3.1"; unfork: "deps(engines): return bifrost to upstream transports/v2.4.0"
func PRBody(a Action, between []Tag) string // a release-notes link per tag, URL-escaped (transports%2Fv2.3.1)
```

CLI: `go run ./tools/enginebump -engine bifrost -body pr-body.md` prints `kind=<k>`, `title=<t>` lines (for
`$GITHUB_OUTPUT`) and the `Note` to stderr. For `bump` it runs `git update-index --cacheinfo 160000,<sha>,<path>` and sets the engine's line in
`engines/versions.env` to the new tag (`check-engine-pins.sh` compares the two);
for `unfork` it also runs `git config -f .gitmodules submodule.<path>.url <Upstream>` and rewrites
`docs/patches.md` with `WithoutPatch`. `-dry-run` changes nothing. `merged` is implemented with
`gh api repos/<o>/<r>/pulls/<n> --jq .merged,.merge_commit_sha` and
`gh api repos/<o>/<r>/compare/<sha>...<tag> --jq .status` (`ahead` or `identical` means contained). Tags come
from `git ls-remote --tags <Upstream>`; the gitlink from `git ls-tree HEAD <path>` — no submodule clone needed.

- [ ] **Step 1: Write the failing tests** (`decide_test.go`, table-driven)

```go
func TestParseLsRemotePeeledWins(t *testing.T) {
    out := "aaa\trefs/tags/v8.0.27\nbbb\trefs/tags/v8.0.28\nccc\trefs/tags/v8.0.28^{}\n"
    got := ParseLsRemote(out)
    want := []Tag{{"v8.0.27", "aaa"}, {"v8.0.28", "ccc"}}
    // assert equal (order-insensitive)
}

func TestLatest(t *testing.T) {
    tags := []Tag{{"transports/v2.2.6","1"},{"transports/v2.3.0","2"},{"transports/v2.10.0","3"},
        {"transports/v2.11.0-prerelease1","4"},{"transports/v3.0.0","5"},{"core/v9.0.0","6"},{"transports/vX","7"}}
    cases := []struct{ prefix string; major int; want string; ok bool }{
        {"transports/v", 2, "transports/v2.10.0", true}, // numeric, not lexical; prerelease and v3 excluded
        {"transports/v", 4, "", false},
    }
    // also: CLIProxyAPI tags {"v8.0.28","v8.0.3","v9.0.0","v7.9.9"} with prefix "v", major 8 → "v8.0.28"
}

func TestCurrentMatchesPeeledCommit(t *testing.T) {
    // gitlink "ccc" with the TestParseLsRemotePeeledWins tags → v8.0.28, true; unknown gitlink → false
}

func TestBetween(t *testing.T) {
    // from v8.0.26 to v8.0.28 → [v8.0.27, v8.0.28]
}

func TestParsePatches(t *testing.T) {
    md := "# Patches\n\n| engine | file | change | why | fork commit | upstream PR | base tag | remove when |\n" +
        "|---|---|---|---|---|---|---|---|\n" +
        "| bifrost | core/schemas/transcriptions.go | add speaker | segments carry speakers | abc123 | https://github.com/maximhq/bifrost/pull/123 | transports/v2.3.0 | in a release |\n"
    // → []Patch{{"bifrost","https://github.com/maximhq/bifrost/pull/123","transports/v2.3.0"}}
    // a non-URL PR, or a missing "base tag" column → error; no table → empty slice, nil
    // WithoutPatch(md,"bifrost") keeps the header and drops the row
}

func TestDecide(t *testing.T) {
    // upstream URL, gitlink at latest                         → none
    // upstream URL, gitlink at v8.0.26, latest v8.0.28        → bump, From v8.0.26, To v8.0.28
    // upstream URL, gitlink at v8.0.28, only v9.0.0 newer     → major (Note names v9.0.0)
    // upstream URL, gitlink untagged                          → error "pin engines/<x> to a release tag by hand"
    // fork URL, patch row, merged=false                       → hold (Note names the PR)
    // fork URL, patch row, merged=true for latest of base major → unfork, To = that latest tag
    // fork URL, no patch row                                  → error "engines/<x> points at a fork with no docs/patches.md row"
    // merged returns an error                                 → error wrapped with %w
}

func TestPRBodyLinksEveryRelease(t *testing.T) {
    // body contains "https://github.com/maximhq/bifrost/releases/tag/transports%2Fv2.3.1" for each tag in between
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./tools/enginebump/ -v`
Expected: FAIL — `undefined: ParseLsRemote` (and the rest).

- [ ] **Step 3: Implement `decide.go`**

`golang.org/x/mod/semver` for comparison (`"v" + strings.TrimPrefix(name, prefix)` must be `semver.IsValid`,
`semver.Prerelease == ""`, `semver.Major == "v<major>"`). The current major comes from `Current`; for a fork it
comes from the patch row's `BaseTag`. Fork detection: `gitmodulesURL` (trailing `.git` and `/` trimmed,
case-insensitive) differs from `Upstream`.

- [ ] **Step 4: Implement `main.go`**

Flags `-engine`, `-body`, `-dry-run`; `context.Context` from `signal.NotifyContext`; `log/slog` for diagnostics;
every exec error wrapped with `%w` and the command line.

- [ ] **Step 5: Run the tests, then a dry run against the real upstreams**

Run: `go test -race ./tools/enginebump/ -v`
Expected: PASS.
Run: `go run ./tools/enginebump -engine cliproxyapi -dry-run`
Expected: `kind=none` or `kind=bump` with `title=deps(engines): bump cliproxyapi to v8.0.<n>` — never a `v9` tag.

- [ ] **Step 6: Commit**

```bash
git add tools/enginebump go.mod go.sum
git commit -m "ci(engines): add the engine bump decision tool"
```

---

### Task 5: the engine bump workflow

**Files:**
- Create: `.github/workflows/engine-bump.yml`

**Interfaces:**
- Consumes: the `tools/enginebump` CLI (Task 4); `REMORA_BOT_APP_ID`, `REMORA_BOT_PRIVATE_KEY` (Task 1); branch
  protection with required `e2e` and auto-merge enabled (`docs/maintaining.md`, Task 1).
- Produces: one pull request per engine on branch `engine-bump/<engine>`, titled by `PRTitle`, set to auto-merge.

- [ ] **Step 1: Write the workflow**

Triggers: `schedule: - cron: '23 5 * * *'` and `workflow_dispatch`. `permissions: {}`. One job `bump`, matrix
`engine: [bifrost, cliproxyapi]`, `fail-fast: false`, `runs-on: ubuntu-24.04`, `permissions: contents: read`.
Steps:
1. `actions/create-github-app-token@v2` → `steps.app.outputs.token`.
2. `actions/checkout@v5` with that token, `submodules: false`.
3. `actions/setup-go@v6` with `go-version-file: go.mod`.
4. `go run ./tools/enginebump -engine ${{ matrix.engine }} -body pr-body.md >> "$GITHUB_OUTPUT" 2>> "$GITHUB_STEP_SUMMARY"`
   with `GH_TOKEN` = the App token (read-only API calls).
5. If `kind` is `bump` or `unfork`: `peter-evans/create-pull-request@v7` with the App token,
   `branch: engine-bump/${{ matrix.engine }}`, `title` and `commit-message` = `steps.decide.outputs.title`,
   `body-path: pr-body.md`, `delete-branch: true`, `add-paths: .gitmodules, docs/patches.md, engines/versions.env` plus the gitlink
   (already staged by the tool).
6. If a pull request was created or updated: `gh pr merge --auto --squash "$PR_URL"` with the App token.

The job never merges by itself: auto-merge waits for every required check, including `e2e` and `quickstart`.
Re-runs update the same branch, so there is never more than one open bump per engine.

- [ ] **Step 2: Lint**

Run: `docker run --rm -v "$PWD:/repo" -w /repo rhysd/actionlint:latest`
Expected: exit 0.

- [ ] **Step 3: Exercise it once on `main` after merge**

Run: `gh workflow run engine-bump.yml && gh run list --workflow engine-bump.yml --limit 1` (check once, do not poll)
Expected: both matrix legs succeed; each either opened a `deps(engines): ...` pull request with auto-merge on, or
wrote a `none`/`hold`/`major` line to the summary.

- [ ] **Step 4: Commit**

```bash
git add .github/workflows/engine-bump.yml
git commit -m "ci(engines): open a daily auto-merging pull request per engine release"
```

---

### Task 6: Dependabot and govulncheck

**Files:**
- Create: `.github/dependabot.yml`
- Create: `.github/workflows/govulncheck.yml`

**Interfaces:**
- Consumes: `go.mod` (root module), `web/` with Bun's text lockfile `bun.lock` (plan 06), `Dockerfile` (plan 01).
- Produces: required check `govulncheck`.

- [ ] **Step 1: Write `.github/dependabot.yml`** (`version: 2`), weekly, one grouped pull request per ecosystem
for minor and patch updates (`groups: all: patterns: ["*"], update-types: [minor, patch]`):

| ecosystem | directory | commit prefix | dev prefix |
|---|---|---|---|
| `gomod` | `/` | `deps` | — |
| `bun` | `/web` | `deps` | `chore` |
| `github-actions` | `/` | `ci` | — |
| `docker` | `/` | `deps` | — |

No `gitsubmodule` entry: the engine bump workflow owns `engines/`. The engines' own `go.mod` files are not
scanned (they are upstream's).

- [ ] **Step 2: Write `.github/workflows/govulncheck.yml`**

Triggers: `pull_request`, `push: branches: [main]`, `schedule: - cron: '41 6 * * 1'`. `permissions: {}`; job
`govulncheck`, `ubuntu-24.04`, `permissions: contents: read`; checkout (no submodules); `golang/govulncheck-action@v1`
with `go-version-file: go.mod`, `go-package: ./...`. A reachable vulnerability fails the job.

- [ ] **Step 3: Validate**

Run: `docker run --rm -v "$PWD:/repo" -w /repo rhysd/actionlint:latest && pipx run check-jsonschema --builtin-schema vendor.dependabot .github/dependabot.yml`
Expected: exit 0; `ok -- validation done`.
Run: `go run golang.org/x/vuln/cmd/govulncheck@latest ./...`
Expected: `No vulnerabilities found.`

- [ ] **Step 4: Commit**

```bash
git add .github/dependabot.yml .github/workflows/govulncheck.yml
git commit -m "ci: add Dependabot for Go, Bun, Actions and Docker, and govulncheck"
```

---

### Task 7: `docs/parity.md` and its gate

**Files:**
- Create: `docs/parity.md`
- Create: `test/docs/parity_test.go`
- Create (only if Step 1 finds gaps): `test/e2e/parity_gaps_test.go` (build tag `e2e`, plan 01's harness)

**Interfaces:**
- Consumes: test functions written by plans 01–05 (names below are the intended ones; Step 1 reconciles them);
  `docs/research/2026-10-14-reference-deployment.md`.
- Produces: `docs/parity.md` in the format below, enforced by `go test ./test/docs/`.

Format: one table, `| id | requirement | source | proof |`. `source` is `MUST <n>`, `MUST NOT <n>` (the n-th bullet
of that list in the research file) or `spec`. `proof` is one or more comma-separated items, each one of:
`` `<dir>.<TestName>` `` (a Go test function in that directory, relative to the repo root); `migration` (proven
against real subscription accounts in the migration's first step, recorded outside this repository);
`exception: <reason>`.

- [ ] **Step 1: Reconcile test names**

The names below were reconciled against the tests plans 01–05 define (`grep -n 'func Test' docs/plans/*.md`);
confirm them against the code on `main`. These have no test in plans 01–05 and are written in
`test/e2e/parity_gaps_test.go` with plan 01's harness and fakes, each asserting exactly its requirement text:
`TestJSONSchemaAcrossFallback`, `TestResponseFormatText`, `TestToolsMultiTurn`, `TestProviderOpenRouterKind`,
`TestProviderOpenAICompatible`, `TestMaxTokensAndReasoningEffort`, `TestConcurrentHeavyRequests`,
`TestLargePromptNoQueueDeadline`, `TestLongStreamNoDeadline`, `TestFailingChainFailsFast`,
`TestResponsesAgentMessage`, `TestTranscriptionLogged`, `TestProviderModelDirect`. The two exceptions are the
spec's: Cline OAuth (M7.6) and clustering (N15).

| id | requirement | source | proof |
|---|---|---|---|
| M1.1 | chat completions, not streamed | MUST 1 | `test/e2e.TestChatNonStreamPersian` |
| M1.2 | stream ends with `[DONE]`; `usage` with `reasoning_tokens` in the final chunk | MUST 1 | `test/e2e.TestChatStreamPersian` |
| M1.3 | one `id` per stream, no synthetic chunks | MUST 1 | `test/e2e.TestChatStreamPersian` |
| M2.1 | strict `json_schema` honoured on every chain member, unfenced | MUST 2 | `test/e2e.TestJSONSchemaPassthrough`, `test/e2e.TestJSONSchemaAcrossFallback` |
| M2.2 | `response_format: text` | MUST 2 | `test/e2e.TestResponseFormatText` |
| M3 | tools: `tool_choice: auto`, multi-turn history, tool calls in streams | MUST 3 | `test/e2e.TestToolCallsInStream`, `test/e2e.TestToolsMultiTurn` |
| M4.1 | embeddings: `dimensions`, batched input, `encoding_format: base64` | MUST 4 | `test/e2e.TestEmbeddings` |
| M4.2 | same embedding model and vectors as before the move | MUST 4 | `migration` |
| M5.1 | transcription: diarization, `context`, verbose `segments` with `speaker` | MUST 5 | `test/e2e.TestTranscriptionSpeakersSurvive` |
| M5.2 | multi-minute jobs | MUST 5 | `test/e2e.TestTranscriptionLongJob` |
| M5.3 | provider file and transcription deleted after every job | MUST 5 | `test/e2e.TestTranscriptionSpeakersSurvive`, `test/e2e.TestTranscriptionJobErrorCleansUp` |
| M5.4 | unknown parameters never dropped silently | MUST 5 | `test/e2e.TestTranscriptionUnknownParam` |
| M6.1 | named aliases, ordered and nestable, across models and providers | MUST 6 | `internal/alias.TestCompile`, `test/e2e.TestNestedAlias` |
| M6.2 | 429 (incl. `insufficient_quota`) and 402 trigger fallback | MUST 6 | `test/e2e.TestFallbackWithinRequest` |
| M7.1 | subscription OAuth, several accounts pooled (Antigravity) | MUST 7 | `migration` |
| M7.2 | subscription OAuth with chat-to-Responses translation (Codex) | MUST 7 | `migration` |
| M7.3 | device-code subscription login (Grok) | MUST 7 | `migration` |
| M7.4 | OpenRouter provider kind | MUST 7 | `test/e2e.TestProviderOpenRouterKind` |
| M7.5 | OpenAI-compatible providers on any base URL (incl. API-key coding-agent providers) | MUST 7 | `test/e2e.TestProviderOpenAICompatible` |
| M7.6 | Cline OAuth | MUST 7 | `exception: neither engine supports it; the reference account is idle and out of credit` |
| M7.7 | Soniox provider | MUST 7 | `test/e2e.TestTranscriptionSpeakersSurvive` |
| M8.1 | one key per consumer; allowlist checked against the name the client sent | MUST 8 | `test/e2e.TestAllowlist` |
| M8.2 | usage and logs per key | MUST 8 | `internal/api.TestKeyUsage`, `test/e2e.TestManagementEndToEnd` |
| M9 | `/v1/models` lists aliases only, with `supported_parameters` incl. `structured_outputs` | MUST 9 | `test/e2e.TestModelsList` |
| M10 | `max_tokens` and `max_output_tokens`; reasoning effort passed through | MUST 10 | `test/e2e.TestMaxTokensAndReasoningEffort` |
| M11 | decisions on `/v1/systemone` and `/v1/decisions`, fallback on 429 | MUST 11 | `test/e2e.TestDecisions` |
| M12.1 | no global single-request admission | MUST 12 | `test/e2e.TestConcurrentHeavyRequests` |
| M12.2 | very large prompts, no short queue deadline | MUST 12 | `test/e2e.TestLargePromptNoQueueDeadline` |
| M12.3 | long streams | MUST 12 | `test/e2e.TestLongStreamNoDeadline` |
| M12.4 | per-alias context-window override | MUST 12 | `test/e2e.TestModelsList` |
| N1 | zero-width characters (ZWNJ) are never stripped; bytes compared | MUST NOT 1 | `test/e2e.TestChatStreamPersian` |
| N2 | no keepalive chunk with its own id | MUST NOT 2 | `test/e2e.TestChatStreamPersian` |
| N3 | unknown multipart parameters never dropped silently | MUST NOT 3 | `test/e2e.TestTranscriptionUnknownParam` |
| N4 | aliases work on `/v1/embeddings` | MUST NOT 4 | `test/e2e.TestEmbeddings` |
| N5 | allowlist checked against the requested alias, everywhere | MUST NOT 5 | `test/e2e.TestAllowlist`, `internal/proxy.TestProxy_Allowlist` |
| N6 | no 200k context cap without override | MUST NOT 6 | `test/e2e.TestModelsList` |
| N7 | no 504 on a huge non-streamed prompt | MUST NOT 7 | `test/e2e.TestLargePromptNoQueueDeadline` |
| N8 | no single heavy-request admission with 503 | MUST NOT 8 | `test/e2e.TestConcurrentHeavyRequests` |
| N9 | one failing member never cools down a whole provider | MUST NOT 9 | `internal/cooling.TestCoolsOnlyFailingMember` |
| N10 | a failing chain fails fast | MUST NOT 10 | `test/e2e.TestFailingChainFailsFast` |
| N11 | provider files are always deleted, including after a crash | MUST NOT 11 | `test/e2e.TestTranscriptionSweep`, `internal/soniox.TestSweepDeletesOldOwned` |
| N12 | `agent_message` input items survive on `/v1/responses` | MUST NOT 12 | `test/e2e.TestResponsesAgentMessage` |
| N13 | `/v1/models` is not a raw catalogue | MUST NOT 13 | `test/e2e.TestModelsList` |
| N14 | members out of credit (402) or capped (429) stop receiving traffic | MUST NOT 14 | `test/e2e.TestCoolingAndRecovery` |
| N15 | clustering and shared OAuth tokens across instances | MUST NOT 15 | `exception: one replica in 0.1 (spec, Non-goals)` |
| N16 | audio requests are logged | MUST NOT 16 | `test/e2e.TestTranscriptionLogged` |
| S1 | `/v1/responses`, streamed and over WebSocket | spec | `internal/proxy.TestProxy_Routes`, `internal/proxy.TestWS_RelaysAndSwapsKey` |
| S2 | `/v1/messages` | spec | `internal/proxy.TestProxy_Routes` |
| S3 | `<provider>/<model>` served directly, no fallback | spec | `test/e2e.TestProviderModelDirect` |
| S4 | serving model in `model`; `x-remora-alias`, `x-remora-served-by`, `x-remora-fallback` | spec | `internal/proxy.TestProxy_ResponseHeaders`, `test/e2e.TestChatStreamPersian` |
| S5 | errors are OpenAI-shaped and never name an engine | spec | `internal/proxy.TestProxy_ErrorMapping`, `test/e2e.TestFallbackAllFail` |
| S6 | dropped Codex parameters announced in `x-remora-dropped-params` | spec | `internal/proxy.TestDroppedParamsHeader` |
| S7 | imported keys keep their exact values; no engine prefix reaches a user | spec | `internal/importer/omniroute.TestConvertKeysVerbatim`, `internal/api.TestCreateKeyShowsValueOnce` |
| S8 | remote-browser OAuth login by pasted address | spec | `internal/accounts.TestCompleteLoginForwardsURL`, `migration` |
| S9 | cooling members recompiled out and restored | spec | `test/e2e.TestCoolingAndRecovery` |
| S10 | reconciliation after an engine restart | spec | `test/e2e.TestReconcileAfterEngineRestart` |
| S11 | `export` then `apply` is a round trip | spec | `internal/config.TestRoundTripNoDiff` |
| S12 | engine version mismatch refuses to start | spec | `internal/supervise.TestStartRefusesMissingOrMismatched` |
| S13 | one exposed port; engines unreachable from outside the container | spec | `test/e2e.TestEnginePortsNotReachableFromOutside` |

Above the table, `docs/parity.md` says in three sentences: what parity means (the reference deployment's MUST and
MUST NOT lists, made generic), what `migration` means, and that `go test ./test/docs/` keeps the file honest.

- [ ] **Step 2: Write the failing tests** in `test/docs/parity_test.go`

```go
type proof struct{ kind, dir, test, note string } // kind: "test" | "migration" | "exception"
type row struct{ id, requirement, source string; proofs []proof }

func parseParity(md string) ([]row, error)
func parseProof(cell string) ([]proof, error)
func referenceCounts(md string) (must, mustNot int, err error) // numbered items under "## MUST", bullets under "## MUST NOT"
func testExists(root, dir, name string) (bool, error)        // regexp `func <name>\(` in <root>/<dir>/*_test.go

func TestParseProof(t *testing.T) {
    // "`test/e2e.TestX`"            → [{test test/e2e TestX}]
    // "`a/b.TestX`, migration"     → two proofs
    // "exception: why"             → [{exception "" "" "why"}]
    // "", "TestX", "`a/b.Foo`"      → error
}
func TestParityEveryRowHasProof(t *testing.T)     // every row: non-empty id, requirement, source; ≥1 proof; ids unique
func TestParityCoversReferenceLists(t *testing.T) // every "MUST n" for n in 1..must and "MUST NOT n" for n in 1..mustNot has a row
func TestParityProofsResolve(t *testing.T)        // every test proof: testExists == true, else t.Errorf("%s: %s.%s not found", id, dir, test)
func TestParityExceptionsAreAllowlisted(t *testing.T) {
    allowed := map[string]bool{"M7.6": true, "N15": true} // a new exception needs a reviewed change here
    // every exception row id is in allowed, and every allowed id is an exception row
}
```

- [ ] **Step 3: Run them to verify they fail**

Run: `go test ./test/docs/ -run 'TestParse|TestParity' -v`
Expected: FAIL — `open ../../docs/parity.md: no such file or directory`.

- [ ] **Step 4: Write `docs/parity.md`** with the reconciled rows from Step 1, and the gap tests if any.

- [ ] **Step 5: Run the gate and the gap tests**

Run: `go test ./test/docs/ -v`
Expected: PASS, including `TestParityProofsResolve`.
Run (only if gap tests were added): `go test -tags e2e -count=1 -run 'TestName1|TestName2' ./test/e2e/...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add docs/parity.md test/docs/parity_test.go test/e2e/parity_gaps_test.go
git commit -m "docs(parity): add the parity checklist with a proof for every row"
```

---

### Task 8: Definition of done — release 0.1.0

**Files:** none (verification only).

**Interfaces:**
- Consumes: everything above, and plans 01–06 merged on `main`.

- [ ] **Step 1: Confirm `main` is green**

Run: `gh run list --branch main --limit 5` (once)
Expected: the latest `ci`, `govulncheck` runs succeeded, including the `quickstart` and `e2e` jobs.
Run: `go test ./test/docs/ -v`
Expected: PASS (parity has a proof per row, every test reference resolves).

- [ ] **Step 2: Release** — **owner's explicit word required** (a public image and release)

Merge the open release-please pull request (`chore(main): release 0.1.0`). Check `release.yml` once with
`gh run list --workflow release.yml --limit 1`; report the link.
Expected: `build` ×2, `verify` ×2 (quick start and full e2e on each released digest), `publish`, `notes` succeed.

- [ ] **Step 3: Verify the published artifact**

Run: `docker buildx imagetools inspect ghcr.io/47vigen/remora:0.1.0`
Expected: platforms `linux/amd64` and `linux/arm64`, each with an attestation manifest.
Run: the `cosign verify` command from Task 3 Step 3 with the 0.1.0 digest.
Expected: `The cosign claims were validated` and a certificate subject under `47vigen/remora/.github/workflows/release.yml@refs/tags/v0.1.0`.
Run: `gh release view v0.1.0`
Expected: the engine table (`bifrost`, `cliproxyapi` versions), the pull-by-digest line, and two SBOM assets.

- [ ] **Step 4: Spec definition-of-done checklist**

1. `docs/parity.md` green, `migration` rows listed for the migration runbook (outside this repository).
2. e2e green on the released image — the `verify` jobs of Step 2.
3. Quick start from a clean machine — the `verify` jobs ran it on fresh runners against the released digests.
4. The reference deployment's migration — outside this repository; not part of this plan.
