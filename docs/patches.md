# Engine patches

An engine listed here runs from a fork branch that carries the patch on top of the upstream release tag in the `base
tag` column. An engine on a fork is never bumped or rebased automatically; when an upstream release contains the
fix, the engine bump workflow opens a pull request that points the submodule back at upstream at that tag and
removes the row. `pending` in the `upstream PR` column means the upstream pull request is not opened yet.

| engine | file | change | why | fork commit | upstream PR | base tag | remove when |
|---|---|---|---|---|---|---|---|
| bifrost | core/schemas/transcriptions.go | add `speaker` to `TranscriptionSegment` | Soniox verbose_json segments carry speakers | 0e2740da5ccf99d08b2c9764dc33ded69a43a457 | https://github.com/maximhq/bifrost/pull/8578 | transports/v2.4.0 | the PR is in a release tag |
| bifrost | core/bifrost.go | no fallback when the primary refuses the request itself (400/409/413/415/422 with an error body), on named chains too (v2.4.0 already stops inferred chains and falls back past `no_key_supports_model`) | a request the primary rejected is otherwise answered by another model | 0e2740da5ccf99d08b2c9764dc33ded69a43a457 | https://github.com/maximhq/bifrost/pull/8755 | transports/v2.4.0 | the PR is in a release tag |
| bifrost | core/schemas/chatcompletions.go | do not emit the synthesised text-only `reasoning_details` entry | reasoning reached clients three times | 0e2740da5ccf99d08b2c9764dc33ded69a43a457 | https://github.com/maximhq/bifrost/pull/8756 | transports/v2.4.0 | the PR is in a release tag |
| bifrost | plugins/routing/rules/engine.go | no routing-engine log entry per rule that did not match | each cost six Sprintf calls, a regex scan and an O(n) append per rule per request; 41 rules took 103 µs per request | 0e2740da5ccf99d08b2c9764dc33ded69a43a457 | https://github.com/maximhq/bifrost/pull/8757 | transports/v2.4.0 | the PR is in a release tag |
| cliproxyapi | sdk/cliproxy/auth/selector_spread.go, sdk/cliproxy/service_config.go, internal/config/config_types.go | `random`, `least-used`, `p2c` selectors on 60 s start counts; `routing.provider-strategies` | per-provider account strategies | 35092b8fc533319a218321e629f2f3af253dcd5e | https://github.com/router-for-me/CLIProxyAPI/pull/6796 | v8.0.51 | the PR is in a release tag |
