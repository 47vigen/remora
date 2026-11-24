# Engine patches

An engine listed here runs from a fork branch that carries the patch on top of the upstream release tag in the `base
tag` column. An engine on a fork is never bumped or rebased automatically; when an upstream release contains the
fix, the engine bump workflow opens a pull request that points the submodule back at upstream at that tag and
removes the row. `pending` in the `upstream PR` column means the upstream pull request is not opened yet.

| engine | file | change | why | fork commit | upstream PR | base tag | remove when |
|---|---|---|---|---|---|---|---|
| bifrost | core/schemas/transcriptions.go | add `speaker` to `TranscriptionSegment` | Soniox verbose_json segments carry speakers | 63817f9081bb34e0534a5e584aede7281ab005a1 | https://github.com/maximhq/bifrost/pull/8578 | transports/v2.6.0 | an upstream release includes speaker metadata |
| bifrost | core/bifrost.go | no fallback when the primary refuses the request itself (400/409/413/415/422 with an error body), on named chains too (v2.4.0 already stops inferred chains and falls back past `no_key_supports_model`) | a request the primary rejected is otherwise answered by another model | 63817f9081bb34e0534a5e584aede7281ab005a1 | https://github.com/maximhq/bifrost/pull/8755 | transports/v2.6.0 | an upstream release includes #8755 or an equivalent fix |
| bifrost | core/schemas/chatcompletions.go | do not emit the synthesised text-only `reasoning_details` entry | reasoning reached clients three times | 63817f9081bb34e0534a5e584aede7281ab005a1 | https://github.com/maximhq/bifrost/pull/8756 | transports/v2.6.0 | an upstream release includes #8756 or an equivalent fix |
| bifrost | plugins/routing/rules/engine.go | no routing-engine log entry per rule that did not match | each cost six Sprintf calls, a regex scan and an O(n) append per rule per request; 41 rules took 103 µs per request | 63817f9081bb34e0534a5e584aede7281ab005a1 | https://github.com/maximhq/bifrost/pull/8757 | transports/v2.6.0 | an upstream release includes #8757 or an equivalent fix |
| bifrost | framework/logstore/rdb.go, transports/bifrost-http/handlers/logging.go | `content=false` on request-log lists | metadata-only reads avoid loading and serializing large payloads | 63817f9081bb34e0534a5e584aede7281ab005a1 | pending | transports/v2.6.0 | an upstream release includes metadata-only request-log lists |
| cliproxyapi | sdk/cliproxy/auth/selector_spread.go, sdk/cliproxy/service_config.go, internal/config/config_types.go | `random`, `least-used`, `p2c` selectors on 60 s start counts; `routing.provider-strategies` | per-provider account strategies | 0c8e4ccf479df07ed8579aed15b18b9edc9f6cd3 | https://github.com/router-for-me/CLIProxyAPI/pull/6796 | v8.1.2 | an upstream release includes #6796 or equivalent per-provider strategy support |
