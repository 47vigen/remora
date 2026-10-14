# Engine patches

An engine listed here runs from a fork branch that carries the patch on top of the upstream release tag in the `base
tag` column. An engine on a fork is never bumped or rebased automatically; when an upstream release contains the
fix, the engine bump workflow opens a pull request that points the submodule back at upstream at that tag and
removes the row. `pending` in the `upstream PR` column means the upstream pull request is not opened yet.

| engine | file | change | why | fork commit | upstream PR | base tag | remove when |
|---|---|---|---|---|---|---|---|
| bifrost | core/schemas/transcriptions.go | add `speaker` to `TranscriptionSegment` | Soniox verbose_json segments carry speakers | 179c29b6265e5b2bbd9d34fc60a6e77bc1264214 | pending | transports/v2.3.0 | the PR is in a release tag |
