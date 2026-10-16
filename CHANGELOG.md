# Changelog

## [0.2.1](https://github.com/47vigen/remora/compare/v0.2.0...v0.2.1) (2026-10-16)


### Performance

* account affinity, thinking levels, front-door fallback and reasoning fixes, pool health ([#47](https://github.com/47vigen/remora/issues/47)) ([3eabf4c](https://github.com/47vigen/remora/commit/3eabf4cd8b69852815e87c6d706b4b5ebb6d4bb1))
* load harness, gateway config and engine settings ([#43](https://github.com/47vigen/remora/issues/43)) ([d36d5af](https://github.com/47vigen/remora/commit/d36d5affb70cd838b56bd3698d785f91e321cf5b))
* **proxy:** hot path — snapshot, one-pass scan, SSE batching, leak fix ([#46](https://github.com/47vigen/remora/issues/46)) ([97b740a](https://github.com/47vigen/remora/commit/97b740a0291955c18df687543cddefee798a9d36))


### Dependencies and engines

* **engines:** bump cliproxyapi to v8.0.31 ([#44](https://github.com/47vigen/remora/issues/44)) ([a6cff60](https://github.com/47vigen/remora/commit/a6cff60ebaab2f94876f6ca2d8e6cdb1547e80c9))

## [0.2.0](https://github.com/47vigen/remora/compare/v0.1.5...v0.2.0) (2026-10-15)


### Features

* **alias:** round-robin strategy; warn on imported members no account serves ([#41](https://github.com/47vigen/remora/issues/41)) ([6401355](https://github.com/47vigen/remora/commit/6401355ecda98e97e5c1dcce394b31e09f4431ac))
* redesign the console ([#37](https://github.com/47vigen/remora/issues/37)) ([f731f56](https://github.com/47vigen/remora/commit/f731f56a3899e775427f3d6fb41c6c9cbfedbe5e))


### Bug Fixes

* **accounts:** nudge the engine when an imported credential lists no models ([#40](https://github.com/47vigen/remora/issues/40)) ([c8d0bf5](https://github.com/47vigen/remora/commit/c8d0bf51884a3e9d2ccb5fc1da4b7fd04094c8f1))
* **config:** apply a remora.yaml in one transaction ([#39](https://github.com/47vigen/remora/issues/39)) ([45ddd6b](https://github.com/47vigen/remora/commit/45ddd6b2125347ba6662b198ab80175b8c361b0d))
* **cooling:** keep log-tail rows for slow subscribers and long requests ([#38](https://github.com/47vigen/remora/issues/38)) ([a7f56ad](https://github.com/47vigen/remora/commit/a7f56ad173099e13aab5932bd90c996d7ca19b90)), closes [#12](https://github.com/47vigen/remora/issues/12) [#13](https://github.com/47vigen/remora/issues/13)

## [0.1.5](https://github.com/47vigen/remora/compare/v0.1.4...v0.1.5) (2026-10-15)


### Bug Fixes

* panel walkthrough against a real deployment ([#35](https://github.com/47vigen/remora/issues/35)) ([6eb735a](https://github.com/47vigen/remora/commit/6eb735aba5d5145751f719175b379f610251ef0a))

## [0.1.4](https://github.com/47vigen/remora/compare/v0.1.3...v0.1.4) (2026-10-15)


### Bug Fixes

* **import:** drop what OmniRoute itself would not serve ([#29](https://github.com/47vigen/remora/issues/29)) ([0ec227c](https://github.com/47vigen/remora/commit/0ec227c75edab374d3fb9bccf4611f3ca9869c5a))
* serve subscription members and OpenCode Go requests ([#31](https://github.com/47vigen/remora/issues/31)) ([7666f33](https://github.com/47vigen/remora/commit/7666f33d29312e859988a104366635b6ecf63d27))

## [0.1.3](https://github.com/47vigen/remora/compare/v0.1.2...v0.1.3) (2026-10-15)


### Bug Fixes

* **alias:** let an alias be named like a provider model ([#27](https://github.com/47vigen/remora/issues/27)) ([0a1c93f](https://github.com/47vigen/remora/commit/0a1c93fc08d603ba74a54a6c965b1d18a780090a))

## [0.1.2](https://github.com/47vigen/remora/compare/v0.1.1...v0.1.2) (2026-10-15)


### Bug Fixes

* **import:** opencode-go is a front-door builtin ([#25](https://github.com/47vigen/remora/issues/25)) ([7e3490a](https://github.com/47vigen/remora/commit/7e3490a1bc5d88bfdf1bfee19997272957795c4e))

## [0.1.1](https://github.com/47vigen/remora/compare/v0.1.0...v0.1.1) (2026-10-15)


### Bug Fixes

* **import:** map OmniRoute's opencode, opencode-go and soniox providers ([#22](https://github.com/47vigen/remora/issues/22)) ([56996a3](https://github.com/47vigen/remora/commit/56996a3beb8c247094bd03f98e5797fbe6862d29))

## 0.1.0 (2026-10-15)


### Features

* **accounts:** subscription accounts, cooling and dropped params (plan 03) ([#8](https://github.com/47vigen/remora/issues/8)) ([087421a](https://github.com/47vigen/remora/commit/087421a362667e9c0f5134ebbd60479730c918e8))
* **api:** management API, remora.yaml and OmniRoute import (plan 05) ([#10](https://github.com/47vigen/remora/issues/10)) ([ee6fc7b](https://github.com/47vigen/remora/commit/ee6fc7b1e79c3edc49ea9ba72460c859e756f104))
* foundation — store, supervisor, image and e2e harness (plan 01) ([#3](https://github.com/47vigen/remora/issues/3)) ([05f737a](https://github.com/47vigen/remora/commit/05f737a59d70aa4aca8dc1ffb2ec8ba7f3f56432))
* inference core: store, reconcile, /v1 proxy and apply (plan 02) ([#5](https://github.com/47vigen/remora/issues/5)) ([75c829b](https://github.com/47vigen/remora/commit/75c829bd71d6482df4c8b04b3502c1e33c01c5b8))
* **soniox:** speech-to-text adapter and speaker patch (plan 04) ([#9](https://github.com/47vigen/remora/issues/9)) ([c15efdd](https://github.com/47vigen/remora/commit/c15efdde0e44e4f38565a1064fd3f4b76b085377))
* **web:** web console embedded in the binary (plan 06) ([#11](https://github.com/47vigen/remora/issues/11)) ([7b1ece9](https://github.com/47vigen/remora/commit/7b1ece90af54ff18b5ad96cbc1fd57a6bb321ae8))


### Dependencies and engines

* **engines:** bump cliproxyapi to v8.0.30 ([#20](https://github.com/47vigen/remora/issues/20)) ([5a177f7](https://github.com/47vigen/remora/commit/5a177f7365365c0b03f6d3cea3720f0e63bcf6ce))
