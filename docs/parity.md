# Parity

Parity means remora covers every item of the MUST and MUST NOT lists in
[the reference deployment](research/2026-10-14-reference-deployment.md), made generic, plus the behaviour the spec
adds on top; each row below names its source and the proof that holds it. A `migration` proof is proven against
real subscription accounts in the migration's first step and recorded outside this repository, because no fake can
stand in for a real account. `go test ./test/docs/` keeps this file honest: every row has a proof,
every list item has a row, every named test exists, and every exception is on a reviewed allowlist.

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
