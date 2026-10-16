# Engine patches

An engine listed here runs from a fork branch that carries the patch on top of the upstream release tag in the `base
tag` column. An engine on a fork is never bumped or rebased automatically; when an upstream release contains the
fix, the engine bump workflow opens a pull request that points the submodule back at upstream at that tag and
removes the row. `pending` in the `upstream PR` column means the upstream pull request is not opened yet.

| engine | file | change | why | fork commit | upstream PR | base tag | remove when |
|---|---|---|---|---|---|---|---|
| bifrost | core/schemas/transcriptions.go | add `speaker` to `TranscriptionSegment` | Soniox verbose_json segments carry speakers | d3ad306803437ddc1950971fcbdf3afa463e9246 | https://github.com/maximhq/bifrost/pull/8578 | transports/v2.3.0 | the PR is in a release tag |
| bifrost | core/bifrost.go | no fallback when the primary refuses the request itself (400/409/413/415/422 with an error body), on named chains too | a request the primary rejected is otherwise answered by another model | d3ad306803437ddc1950971fcbdf3afa463e9246 | pending | transports/v2.3.0 | the PR is in a release tag |
| bifrost | core/schemas/chatcompletions.go | do not emit the synthesised text-only `reasoning_details` entry | reasoning reached clients three times | d3ad306803437ddc1950971fcbdf3afa463e9246 | pending | transports/v2.3.0 | the PR is in a release tag |
| cliproxyapi | sdk/cliproxy/auth/selector_spread.go, sdk/cliproxy/service_config.go, internal/config/config_types.go | `random`, `least-used`, `p2c` selectors on 60 s start counts; `routing.provider-strategies` | per-provider account strategies | 2d4f974a34823e1fd48ce89578102777793a7eb9 | pending | v8.0.31 | the PR is in a release tag |
