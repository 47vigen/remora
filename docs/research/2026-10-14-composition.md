# Composing engines from source: one binary or one image

Date: 2026-10-14. Sources: agentgateway main `e523afb`, CLIProxyAPI v8 (`…/CLIProxyAPI/v8`, Go 1.26),
cliproxy-rs 0.2.5, `ahoo/cpa-plugin-systemone`, read at their heads.

## The question

remora wants one artifact built from upstream source, not several images with their own dependencies. Two shapes
were possible: one process (everything linked into one binary) or one image holding several processes.

## Findings

- **agentgateway as a library:** `main.rs` calls `agentgateway_app::run()`, which owns the runtime and has no hooks.
  Providers are a closed enum; no provider registration API. Workspace `publish = false`.
- **CLIProxyAPI as a library:** `sdk/cliproxy` (`NewBuilder().WithConfig()...Build()`, `svc.Run(ctx)`) embeds the
  whole service and accepts extra gin routes. The executors live under `internal/`, so they cannot be imported on
  their own. `docs/sdk-usage.md` is stale (still v6). With two module-path majors in five months, an embedding
  would break on every upgrade.
- **Plugins:** CLIProxyAPI loads C-ABI `.so` plugins via dlopen; needs cgo and glibc ("never alpine/musl").
  `cpa-plugin-systemone` pins v7 while upstream is v8.
- **cliproxy-rs crates in one Rust binary with agentgateway:** shared versions line up (tokio 1.53, hyper 1.11,
  axum 0.8, rustls 0.23). Risks: two TLS stacks (`aws-lc-sys` and a BoringSSL fork via `btls-sys`) in one binary,
  `[patch.crates-io]` sets that only apply at the workspace root, and full LTO over both trees. cliproxy-rs is 11
  days old with a release-candidate HTTP client.

## Glue estimates

| shape | glue | risk |
|---|---|---|
| A. one image, engines built from source, several processes | about 100 lines of Dockerfile + a small supervisor, zero engine code | lowest |
| B. one Rust binary (agentgateway + cliproxy-rs crates) | 150 to 250 lines to start a second listener; 400 to 700 lines and a fork to call executors in-process | highest |
| C. one Go binary (CLIProxyAPI SDK + own gateway) | 100 to 200 lines of glue plus a whole gateway to write | loses the front door |

## Decision

Shape A. Loopback HTTP between processes costs microseconds against upstream latencies of one to four seconds.
The engines stay byte-identical to upstream, so following their releases is a submodule bump plus an end-to-end
gate, not a merge. Base image must be glibc. remora's own binary is the supervisor (see the spec).
