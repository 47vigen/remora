# Speech-to-text (Soniox adapter and the `speaker` patch) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A client sends an OpenAI-style multipart `POST /v1/audio/transcriptions` with `model: soniox/<model>` to remora and gets OpenAI `verbose_json` back with `segments[].speaker`, while no Soniox file or transcription outlives the request (or, after a crash, one hour).

**Architecture:** `internal/soniox` is an `http.Handler` that remora serves on `127.0.0.1:18090`. It turns one OpenAI multipart request into Soniox's async flow (upload, create, poll, fetch, delete) and groups Soniox tokens into words and speaker segments. Reconcile registers it in Bifrost as the custom OpenAI-type provider `soniox` (transcription only); Bifrost forwards the provider key as `Authorization: Bearer`, so the adapter holds no key per request. A sweeper deletes leftovers tagged with remora's `client_reference_id` prefix. Bifrost drops `speaker` from verbose segments, so the plan carries a one-field patch in a fork until upstream merges it.

**Tech Stack:** Go 1.27 standard library (`net/http`, `mime/multipart`, `encoding/json`, `log/slog`), Bifrost (Go, built from the submodule), Docker Compose e2e with the fakes image.

**Spec:** `docs/specs/2026-10-14-remora-0.1-design.md` (section "Speech-to-text: the Soniox adapter"; e2e list under "Engineering practice"). Shared contracts: `docs/plans/README.md`.

## Global Constraints

- Module `github.com/47vigen/remora`, Go 1.27; the adapter lives in `internal/soniox`; engines are never imported, only built.
- Soniox adapter port `127.0.0.1:18090`, overridable through `REMORA_PORT_SPEECH`.
- "Unknown parameters are forwarded or rejected with a clear error, never dropped."
- "A periodic sweep deletes any Soniox file or transcription older than one hour that remora created."
- Output: OpenAI `verbose_json` with `text`, `language`, `duration`, `words`, and `segments` (`id`, `speaker`, `start`, `end`, `text`).
- No engine name in any error a user sees ("Leaking the engines" anti-goal). The provider name `soniox` is fine.
- Errors use the OpenAI error shape `{"error":{"message","type","code"}}`.
- Never rewrite content: token text is concatenated byte for byte; only leading/trailing `unicode.IsSpace` runes are trimmed. ZWNJ (U+200C) is not whitespace and must survive.
- No real provider and no real key in any test. Unit tests `go test -race ./...`; e2e under build tag `e2e`.
- An engine patch lives in the fork `github.com/47vigen/bifrost` only until upstream merges it, and is listed in `docs/patches.md`.
- Conventional Commits with scopes (`feat(soniox):`, `test(e2e):`, `build(engines):`); branch `plan-04-speech-to-text`.
- No owner host names, domains, product names or secrets anywhere.

## Verified facts this plan relies on

Soniox (fetched 2026-10-14 from `soniox.com/docs/llms.txt` and `/docs/api-reference/stt/{transcriptions,files}/*.mdx`):

| call | path | notes |
|---|---|---|
| upload | `POST /v1/files` multipart `file`, `client_reference_id` | 201 `{id, filename, size, created_at, client_reference_id}` |
| create | `POST /v1/transcriptions` JSON | `model` (required), `file_id`, `language_hints []string`, `language_hints_strict bool`, `enable_speaker_diarization bool`, `enable_language_identification bool`, `translation object`, `context` (string **or** object `{general,text,terms,translation_terms}`), `webhook_*`, `client_reference_id` (max 256). 201 with `status: queued` |
| poll | `GET /v1/transcriptions/{id}` | `status` ∈ `queued, processing, completed, error`; `error_type`, `error_message`, `audio_duration_ms`, `file_id`, `created_at`, `client_reference_id` |
| fetch | `GET /v1/transcriptions/{id}/transcript` | `{id, text, tokens[]{text, start_ms, end_ms, confidence, speaker?, language?, is_audio_event?, translation_status?}}`; tokens are sub-word (`"Hel"`, `"lo"`) |
| delete | `DELETE /v1/transcriptions/{id}`, `DELETE /v1/files/{id}` | 204. "Cannot delete transcriptions that are currently processing." Deleting a file before its transcription finishes fails it with `file_not_found` |
| list | `GET /v1/transcriptions`, `GET /v1/files` | `limit`, `cursor` query; body `{transcriptions|files: [...], next_page_cursor}` |
| errors | any | `{status_code, error_type, message, validation_errors?, request_id}` (LiteLLM reads `error_message`; the real field on HTTP errors is `message`) |
| limits | | 1,000 files / 10 GB, 100 pending, 2,000 transcriptions; leftovers auto-deleted only after 30 days; max file 300 min. Current async model id in docs: `stt-async-v5` |

`client_reference_id` exists on both files and transcriptions and is returned by list, so it is how remora recognises its own objects.

Bifrost (`engines/bifrost` at the pinned commit; read at `72457fe`):

- Custom OpenAI-type providers send `Authorization: Bearer <provider key>` (`core/providers/openai/openai.go:239-245`, used at `:3154`) to `base_url + "/v1/audio/transcriptions"` (`openai.go:110-117`, `:3157`). So **Bifrost forwards the key**; decision below.
- The HTTP handler parses only `model, file, language, prompt, response_format, stream, fallbacks` (`file_format` is known and ignored); every other non-empty multipart field goes to `ExtraParams` as a **string, first value only** (`transports/bifrost-http/handlers/inference.go:380-389,1878-1894`) and is re-sent as a multipart field (`core/providers/openai/transcription.go:101-124`). The file field is written last (`transcription.go:126`).
- `verbose_json` is decoded into `BifrostTranscriptionResponse` (`openai.go:3381`). `TranscriptionWord` already has `Speaker *string` (`core/schemas/transcriptions.go:205-210`); `TranscriptionSegment` has no `speaker` (`:213-224`), so it is dropped.
- `response_format=diarized_json` keeps speakers via `TranscriptionDiarizedSegment` (`:230-237`), but the decoder `openAIDiarizedTranscriptionResponse` (`openai.go:3175-3181`) has no `language` and no `words`, ids become strings (`"seg_0"`), it adds `is_diarized`, and the client must ask for `diarized_json`. Sending string ids under `verbose_json` triggers the sniff in `UnmarshalJSON` (`transcriptions.go:107-121`) with the same losses. **Neither avoids the patch** for a `verbose_json` client; `words[].speaker` survives today but is not segments.
- Provider create payload: `POST /api/providers` `{provider, network_config, custom_provider_config}` (`handlers/providers.go:147-160`); `network_config.default_request_timeout_in_seconds` (default 300, `core/schemas/provider.go:16,66`), `max_retries`, `allow_private_network` (`provider.go:78`); `custom_provider_config.{base_provider_type, is_key_less, allowed_requests}` with `allowed_requests.transcription` (`provider.go:563-571,372`). An `allowed_requests` object makes every unset operation false (`provider.go:419`).

## Decisions

- **Key path:** Bifrost forwards the Soniox key as the provider key; the adapter uses the `Authorization` bearer of each request and never reads the store per request. The key stays one secret in plan 02's provider store, pushed to Bifrost like any API-key provider, and multi-key selection stays Bifrost's job. The sweeper, which has no request, reads keys from the store through a `KeySource`.
- **Ownership tag:** every upload and transcription gets `client_reference_id = "remora:" + 32 hex chars`. The sweeper only touches objects with prefix `remora:`.
- **Cleanup order:** after the job ends for any reason, delete the **file first**, then the transcription, both with `context.WithoutCancel(ctx)` and a 30 s timeout each; 404 counts as deleted. File-first makes an abandoned job fail fast (`file_not_found`) so the sweeper can delete it; on success the order does not matter.
- **Timeouts:** adapter deadline default 30 min; the Bifrost provider gets `default_request_timeout_in_seconds = deadline + 60`.

## Review Focus

1. Client disconnects during a long poll: the request context is cancelled, yet the file and transcription must still be deleted. Test `TestHandlerCleansUpWhenClientCancels` (Task 5).
2. Diarization off (no `speaker` on tokens): segments split by pause and length only, `speaker` omitted, not `""`. Test `TestGroupSegmentsNoSpeaker` (Task 4).
3. A `response_format` the adapter cannot honour (`srt`, `vtt`, `diarized_json`) must be a clear 400, not a silent `json`. Test `TestParseRejectsResponseFormat` (Task 3).
4. Two Soniox keys in the store: the sweeper must clean both accounts and keep going when one key fails. Test `TestSweepEachKeyIndependently` (Task 6).
5. A Soniox error body that echoes the request's credential must not reach the client or the log. Test `TestClientErrorRedactsKey` (Task 2).

---

## Names consumed from plans 01 and 02

Every name this plan takes from plans 01 and 02, resolved against those plans.

```go
// internal/config (plan 01): config.Config; Ports.Soniox is the adapter port (env REMORA_PORT_SPEECH, default 18090).
//   This plan adds the Soniox* fields below to it.
// internal/store (plan 02): provider kind store.ProviderKind "soniox" (already in the providers CHECK constraint);
func (s *Store) ProviderKeysByKind(ctx context.Context, kind ProviderKind) ([]string, error) // plaintext secrets of enabled keys
// internal/engine/bifrost (plan 02): bifrost.Provider{Name, BaseType, BaseURL, AllowPrivateNetwork, AllowedRequests,
//   RequestTimeout}, pushed by the reconciler through CreateProvider/UpdateProvider; keys follow plan 02's key path.
// internal/reconcile (plan 02): desired.go maps each store provider to a bifrost.Provider; until this plan, kind
//   "soniox" fails validation with ErrInvalid. This plan adds the "soniox" case.
// internal/proxy (plan 02): no response or write timeout on /v1 (TestProxy_NoResponseTimeout), so a transcription
//   may run for the adapter deadline + 60 s.
// test/e2e (plans 01, 02): helpers remoraURL, fakesURL, execIn, resetFakes, waitHealthy; seeding with
//   `remora apply -f` through execIn; fakes on port 9090, one upstream per /u/{upstream} prefix.
```

---

### Task 1: Soniox fake as an importable package with scripting

The fakes binary from plan 01 serves a Soniox fake. Move or extend it into a package that unit tests can import, so the adapter's unit tests and the e2e image use the same fake.

**Files:**
- Create or modify: `test/fakes/soniox/fake.go` (handler and state), `test/fakes/soniox/control.go` (control API)
- Modify: `test/fakes/main.go` (mount `soniox.New()` on its existing Soniox route prefix)
- Test: `test/fakes/soniox/fake_test.go`

**Interfaces:**
- Produces:

```go
package soniox // test/fakes/soniox

type Token struct {
    Text              string `json:"text"`
    StartMs           int64  `json:"start_ms"`
    EndMs             int64  `json:"end_ms"`
    Confidence        float64 `json:"confidence"`
    Speaker           string `json:"speaker,omitempty"`
    Language          string `json:"language,omitempty"`
    TranslationStatus string `json:"translation_status,omitempty"`
}
type Script struct {
    Tokens          []Token       `json:"tokens"`
    Text            string        `json:"text"`
    AudioDurationMs int64         `json:"audio_duration_ms"`
    ProcessingFor   time.Duration `json:"processing_for"`   // wall time in "processing" after create (JSON: Go duration string)
    FailWith        string        `json:"fail_with"`        // non-empty: job ends status "error" with this error_message
    HTTPStatus      int           `json:"http_status"`      // non-zero: create answers this status with Soniox error body
    EchoAuthInError bool          `json:"echo_auth_in_error"` // error message includes the Authorization header value
}
type Object struct {
    ID, Kind, Key, ClientReferenceID, Status, FileID string // Kind "file"|"transcription"; Key = bearer that created it
    CreatedAt time.Time
    CreateBody json.RawMessage // transcriptions only: the exact JSON body received
}
type State struct {
    Live    []Object `json:"live"`
    Deleted []Object `json:"deleted"`
    Polls   int      `json:"polls"`
}
type Fake struct{ /* mutex-guarded */ }
func New() *Fake
func (f *Fake) ServeHTTP(w http.ResponseWriter, r *http.Request) // Soniox API + /_control/soniox/*
func (f *Fake) SetScript(s Script)
func (f *Fake) Seed(o Object)             // inject a leftover (any CreatedAt, any status)
func (f *Fake) State() State
func (f *Fake) Reset()
```

Control API (JSON): `PUT /_control/soniox/script` (body `Script`), `POST /_control/soniox/seed` (body `Object`), `GET /_control/soniox/state`, `POST /_control/soniox/reset`.

Fake rules (mirror the verified Soniox behaviour): any request without `Authorization: Bearer <non-empty>` → 401 Soniox error body; `DELETE` of a transcription whose status is `queued|processing` → 409 Soniox error body `error_type: invalid_request`; deleting a file whose transcription is still processing sets that transcription to `error` with `error_type: file_not_found`; unknown id → 404; list endpoints page 2 items per page with `next_page_cursor` so pagination is exercised; list returns only objects whose `Key` equals the caller's bearer.

- [ ] **Step 1: Write the failing tests**

```go
func TestFakeLifecycle(t *testing.T) // upload → create → poll completed → transcript → delete both; State().Live empty, len(Deleted)==2
func TestFakeProcessingFor(t *testing.T) // ProcessingFor 200ms: first GET status=="processing"; after 250ms status=="completed"
func TestFakeDeleteProcessingIs409(t *testing.T)
func TestFakeDeleteFileFailsProcessingJob(t *testing.T) // status becomes "error", error_type=="file_not_found"
func TestFakeListPaginatesPerKey(t *testing.T) // 3 seeded for key "a", 1 for "b": key "a" sees 3 across 2 pages
func TestFakeRecordsCreateBody(t *testing.T) // State().Live[i].CreateBody equals the sent JSON byte for byte
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test -race ./test/fakes/soniox/`
Expected: FAIL (`undefined: New` or missing behaviour)

- [ ] **Step 3: Implement `Fake` in `test/fakes/soniox/fake.go` and `control.go`; mount it in `test/fakes/main.go`**

IDs are random UUIDv4 strings; `created_at` RFC 3339 UTC.

- [ ] **Step 4: Run to verify they pass**

Run: `go test -race ./test/fakes/...`
Expected: `ok  github.com/47vigen/remora/test/fakes/soniox`

- [ ] **Step 5: Commit**

```bash
git add test/fakes
git commit -m "test(fakes): scriptable Soniox fake with lifecycle, pagination and control API"
```

---

### Task 2: Soniox API client

**Files:**
- Create: `internal/soniox/client.go`, `internal/soniox/types.go`
- Test: `internal/soniox/client_test.go`

**Interfaces:**
- Consumes: `test/fakes/soniox.New()` (Task 1) in tests.
- Produces:

```go
const RefPrefix = "remora:"

type File struct {
    ID                string    `json:"id"`
    Filename          string    `json:"filename"`
    ClientReferenceID string    `json:"client_reference_id"`
    CreatedAt         time.Time `json:"created_at"`
}
type Transcription struct {
    ID                string    `json:"id"`
    Status            string    `json:"status"` // queued|processing|completed|error
    FileID            string    `json:"file_id"`
    ClientReferenceID string    `json:"client_reference_id"`
    ErrorType         string    `json:"error_type"`
    ErrorMessage      string    `json:"error_message"`
    AudioDurationMs   int64     `json:"audio_duration_ms"`
    CreatedAt         time.Time `json:"created_at"`
}
type Token struct {
    Text              string  `json:"text"`
    StartMs           int64   `json:"start_ms"`
    EndMs             int64   `json:"end_ms"`
    Confidence        float64 `json:"confidence"`
    Speaker           string  `json:"speaker"`
    Language          string  `json:"language"`
    TranslationStatus string  `json:"translation_status"`
}
type Transcript struct {
    ID     string  `json:"id"`
    Text   string  `json:"text"`
    Tokens []Token `json:"tokens"`
}
type CreateRequest struct {
    Model                        string          `json:"model"`
    FileID                       string          `json:"file_id"`
    ClientReferenceID            string          `json:"client_reference_id"`
    LanguageHints                []string        `json:"language_hints,omitempty"`
    LanguageHintsStrict          *bool           `json:"language_hints_strict,omitempty"`
    EnableSpeakerDiarization     *bool           `json:"enable_speaker_diarization,omitempty"`
    EnableLanguageIdentification *bool           `json:"enable_language_identification,omitempty"`
    Context                      json.RawMessage `json:"context,omitempty"`     // a JSON string or object
    Translation                  json.RawMessage `json:"translation,omitempty"` // a JSON object
}

// APIError is a non-2xx answer from Soniox. Error() never contains the key.
type APIError struct {
    Status  int
    Type    string // Soniox error_type
    Message string // Soniox message, redacted
}
func (e *APIError) Error() string // "soniox <op>: <status> <type>: <message>"

type Client struct{ /* baseURL string; hc *http.Client */ }
func NewClient(baseURL string, hc *http.Client) *Client
func (c *Client) UploadFile(ctx context.Context, key, ref, filename string, body io.Reader) (File, error)
func (c *Client) CreateTranscription(ctx context.Context, key string, req CreateRequest) (Transcription, error)
func (c *Client) GetTranscription(ctx context.Context, key, id string) (Transcription, error)
func (c *Client) GetTranscript(ctx context.Context, key, id string) (Transcript, error)
func (c *Client) DeleteFile(ctx context.Context, key, id string) error          // 404 → nil
func (c *Client) DeleteTranscription(ctx context.Context, key, id string) error // 404 → nil
func (c *Client) ListFiles(ctx context.Context, key string) ([]File, error)               // follows next_page_cursor until null/""
func (c *Client) ListTranscriptions(ctx context.Context, key string) ([]Transcription, error)
func redact(s, key string) string // replaces every occurrence of key with "[REDACTED]"; key=="" → s
```

`UploadFile` streams `body` through `io.Pipe` + `multipart.Writer` (fields `client_reference_id`, then `file`), so the audio is not buffered twice. Errors are wrapped with `%w` and carry the operation name.

- [ ] **Step 1: Write the failing tests** (against `httptest.NewServer(fake.New())`)

```go
func TestClientRoundTrip(t *testing.T) {
    // upload with ref "remora:abc" → create → GetTranscription until completed → GetTranscript
    // assert f.ClientReferenceID == "remora:abc"; tr.Status == "queued"; transcript.Tokens equal the script's tokens
}
func TestClientDeleteNotFoundIsNil(t *testing.T) // DeleteFile(ctx,"k","missing") == nil; DeleteTranscription likewise
func TestClientAPIError(t *testing.T) {
    // script HTTPStatus 429: var apiErr *APIError; errors.As(err,&apiErr); apiErr.Status==429
}
func TestClientErrorRedactsKey(t *testing.T) {
    // script HTTPStatus 400 + EchoAuthInError, key "sk-test-secret": strings.Contains(err.Error(), "sk-test-secret") == false
    // strings.Contains(err.Error(), "[REDACTED]") == true
}
func TestClientListFollowsCursor(t *testing.T) // 3 seeded files for key "k" → len(ListFiles)==3
func TestCreateRequestOmitsUnset(t *testing.T) {
    // json.Marshal(CreateRequest{Model:"m",FileID:"f",ClientReferenceID:"r"}) ==
    // `{"model":"m","file_id":"f","client_reference_id":"r"}`
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test -race ./internal/soniox/ -run 'TestClient|TestCreateRequest'`
Expected: FAIL (`undefined: NewClient`)

- [ ] **Step 3: Implement `client.go` and `types.go`**

- [ ] **Step 4: Run to verify they pass**

Run: `go test -race ./internal/soniox/ -run 'TestClient|TestCreateRequest'`
Expected: `ok  github.com/47vigen/remora/internal/soniox`

- [ ] **Step 5: Commit**

```bash
git add internal/soniox
git commit -m "feat(soniox): async API client with pagination and key redaction"
```

---

### Task 3: Multipart parameters → Soniox request

**Files:**
- Create: `internal/soniox/params.go`
- Test: `internal/soniox/params_test.go`

**Interfaces:**
- Consumes: `CreateRequest` (Task 2).
- Produces:

```go
type request struct {
    Create         CreateRequest // Model, hints, flags, context, translation; FileID/ClientReferenceID filled later
    ResponseFormat string        // "json" (default) | "verbose_json" | "text"
    Language       string        // the OpenAI language field, for the language fallback in Task 4
}
// ParamError is a client mistake; the handler answers 400 with Error() as the message.
type ParamError struct{ Field, Reason string }
func (e *ParamError) Error() string // `invalid parameter "<field>": <reason>` or `unsupported parameter "<field>"`
func parseParams(form *multipart.Form) (request, error)
```

Field table (every multipart value field; `file` is handled by the handler). A field listed nowhere is a `ParamError{Reason: "unsupported"}`; nothing is ever ignored.

| field | rule |
|---|---|
| `model` | required, non-empty → `Create.Model` |
| `language` | → `Language`; prepended to `language_hints` unless already present |
| `prompt` | → `Create.Context` as a JSON string; if `context` is also present → `ParamError` "use either prompt or context" |
| `response_format` | `json`, `verbose_json`, `text`; anything else → `ParamError` "supported: json, verbose_json, text" |
| `timestamp_granularities[]`, `timestamp_granularities` | each value must be `word` or `segment`; accepted because `verbose_json` always carries both |
| `enable_speaker_diarization`, `enable_language_identification`, `language_hints_strict` | `strconv.ParseBool` (accepts `true/false/1/0/True/FALSE`…); parse failure → `ParamError` |
| `language_hints` | a JSON array of strings if the value starts with `[`, else comma-separated, trimmed, empties dropped. (Bifrost forwards only the first value of a repeated field, so one field carries all hints.) |
| `context` | a JSON object if the value starts with `{` (must be valid JSON), else a JSON string |
| `translation` | must be a JSON object |
| `temperature` | `ParamError` "Soniox has no sampling temperature" |
| `stream` | `true` → `ParamError` "streaming is not supported"; `false` accepted |
| `audio_url`, `file_id`, `client_reference_id`, `webhook_url`, `webhook_auth_header_name`, `webhook_auth_header_value` | `ParamError` "managed by remora" (the adapter owns the object lifecycle and its cleanup tag; this also keeps webhook secrets out of every log) |

- [ ] **Step 1: Write the failing tests** (table-driven, building `*multipart.Form` literals)

```go
func TestParseParams(t *testing.T) {
    cases := []struct{ name string; values map[string][]string; want request }{
        {"diarization and hints", map[string][]string{"model":{"stt-async-v5"},"enable_speaker_diarization":{"True"},"language":{"fa"},"language_hints":{"en, fa"}},
            request{Create: CreateRequest{Model:"stt-async-v5", EnableSpeakerDiarization: ptr(true), LanguageHints: []string{"en","fa"}}, ResponseFormat:"json", Language:"fa"}},
        {"json array hints", map[string][]string{"model":{"m"},"language_hints":{`["fa","en"]`}}, /* LanguageHints == []string{"fa","en"} */},
        {"language prepended", map[string][]string{"model":{"m"},"language":{"de"},"language_hints":{"fa"}}, /* LanguageHints == []string{"de","fa"} */},
        {"prompt becomes context string", map[string][]string{"model":{"m"},"prompt":{"cardiology"}}, /* string(Create.Context) == `"cardiology"` */},
        {"context object", map[string][]string{"model":{"m"},"context":{`{"terms":["Soniox"]}`}}, /* string(Create.Context) == `{"terms":["Soniox"]}` */},
        {"verbose with granularities", map[string][]string{"model":{"m"},"response_format":{"verbose_json"},"timestamp_granularities[]":{"segment"}}, /* ResponseFormat == "verbose_json" */},
    }
}
func TestParseRejects(t *testing.T) {
    // each case: errors.As(err, &pe) and pe.Field == want
    // {"foo":"1"} → Field "foo"; {"temperature":"0"}; {"enable_speaker_diarization":"yes"}; {"context":"{bad"};
    // {"translation":"x"}; {"prompt":"a","context":"b"}; {"webhook_auth_header_value":"s"}; {"stream":"true"}; missing model → Field "model"
}
func TestParseRejectsResponseFormat(t *testing.T) // "srt","vtt","diarized_json" → Field "response_format"
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test -race ./internal/soniox/ -run TestParse`
Expected: FAIL (`undefined: parseParams`)

- [ ] **Step 3: Implement `parseParams` in `params.go`**

- [ ] **Step 4: Run to verify they pass**

Run: `go test -race ./internal/soniox/ -run TestParse`
Expected: `ok  github.com/47vigen/remora/internal/soniox`

- [ ] **Step 5: Commit**

```bash
git add internal/soniox/params.go internal/soniox/params_test.go
git commit -m "feat(soniox): map OpenAI multipart fields to Soniox, reject unknown ones"
```

---

### Task 4: Tokens → words, speaker segments, verbose_json

**Files:**
- Create: `internal/soniox/transcript.go`
- Test: `internal/soniox/transcript_test.go`

**Interfaces:**
- Consumes: `Token`, `Transcript`, `Transcription` (Task 2).
- Produces:

```go
type Word struct {
    Word    string  `json:"word"`
    Start   float64 `json:"start"`
    End     float64 `json:"end"`
    Speaker string  `json:"speaker,omitempty"`
}
type Segment struct {
    ID      int     `json:"id"`
    Speaker string  `json:"speaker,omitempty"`
    Start   float64 `json:"start"`
    End     float64 `json:"end"`
    Text    string  `json:"text"`
}
type Usage struct {
    Type    string  `json:"type"`    // "duration"
    Seconds float64 `json:"seconds"`
}
type VerboseJSON struct {
    Task     string    `json:"task"` // "transcribe"
    Language string    `json:"language,omitempty"`
    Duration float64   `json:"duration"`
    Text     string    `json:"text"`
    Words    []Word    `json:"words"`
    Segments []Segment `json:"segments"`
    Usage    Usage     `json:"usage"`
}
type Grouping struct {
    Pause      time.Duration // default 1s
    MaxSegment time.Duration // default 30s
}
func groupWords(tokens []Token) []Word
func groupSegments(tokens []Token, g Grouping) []Segment
func pickLanguage(tokens []Token, requested string, hints []string) string
func buildVerbose(tr Transcription, ts Transcript, req request, g Grouping) VerboseJSON
```

Algorithm (the signatures do not determine it):

```
src := tokens with TranslationStatus != "translation"     // translated tokens have no audio timing
startsWord(t) := t.Text != "" && unicode.IsSpace(first rune of t.Text)

groupWords:  a new word starts at src[0], at startsWord(t), or when t.Speaker != current word's speaker.
             word text = strings.TrimSpace(concatenated token texts); start = first StartMs/1000, end = last EndMs/1000.
             words whose trimmed text is "" are dropped.

groupSegments: for each t in src (prev = previous token, cur = open segment):
    split := cur == nil
          || t.Speaker != cur.speaker
          || startsWord(t) && t.StartMs-prev.EndMs >= g.Pause.Milliseconds()
          || startsWord(t) && t.EndMs-cur.startMs  >  g.MaxSegment.Milliseconds()
    if split { close(cur); cur = {speaker: t.Speaker, startMs: t.StartMs} }
    cur.text += t.Text; cur.endMs = t.EndMs
  close(cur): text = strings.TrimSpace(cur.text); if text != "" append Segment{ID: len(out), ...}
  Pause and length split only at a word boundary, so a word is never cut; a speaker change always splits.

pickLanguage: the most frequent non-empty Token.Language in src (tie: the one seen first);
              else requested; else hints[0]; else "".
buildVerbose: Text = ts.Text verbatim; Duration = tr.AudioDurationMs/1000, or the last src EndMs/1000 when 0;
              Usage = {"duration", Duration}; Words/Segments never nil (empty slices).
```

`shortcut:` word boundaries come from leading whitespace, so scripts written without spaces (Chinese, Japanese) yield one word per speaker run; upgrade to per-token words when a user needs CJK word timing.

- [ ] **Step 1: Write the failing tests**

```go
func tok(text string, s, e int64, spk string) Token

func TestGroupSegmentsBySpeaker(t *testing.T) {
    toks := []Token{tok("Hel",0,100,"1"), tok("lo",100,200,"1"), tok(" there",250,500,"1"), tok(" Hi",600,800,"2"), tok("!",800,850,"2")}
    got := groupSegments(toks, Grouping{Pause: time.Second, MaxSegment: 30*time.Second})
    want := []Segment{{ID:0,Speaker:"1",Start:0,End:0.5,Text:"Hello there"},{ID:1,Speaker:"2",Start:0.6,End:0.85,Text:"Hi!"}}
    // reflect.DeepEqual(got, want)
}
func TestGroupSegmentsSplitsOnPause(t *testing.T) // same speaker, " a" 0-100, " b" 1200-1300 → 2 segments; gap 999ms → 1 segment
func TestGroupSegmentsPauseNeverCutsWord(t *testing.T) // "wor" 0-100, "ld" 1500-1600 (no leading space) → 1 segment "world"
func TestGroupSegmentsMaxLength(t *testing.T) // 40 tokens " w", each 100ms long, 800ms gap between them (below Pause), MaxSegment 30s → 2 segments, each End-Start <= 30
func TestGroupSegmentsNoSpeaker(t *testing.T) // Speaker "" everywhere → segments split by pause; json.Marshal(seg) has no "speaker" key
func TestGroupSegmentsSkipsTranslation(t *testing.T) // TranslationStatus "translation" tokens absent from segment text
func TestGroupSegmentsKeepsZWNJ(t *testing.T) {
    toks := []Token{tok(" می",0,100,"1"), tok("‌خو",100,200,"1"), tok("اهم",200,300,"1")}
    // groupSegments(...)[0].Text == "می‌خواهم" (bytes.Equal on []byte)
    // groupWords(toks)[0].Word == "می‌خواهم"
}
func TestGroupSegmentsIDsContiguous(t *testing.T) // a whitespace-only run between speakers is dropped; IDs are 0..n-1
func TestGroupWords(t *testing.T) // "Hel","lo"," there",","," Hi" → words "Hello","there,","Hi"; a speaker change mid-run starts a new word
func TestPickLanguage(t *testing.T) // {fa,fa,en} → "fa"; {en,fa} tie → "en"; none + requested "de" → "de"; none + hints ["fr"] → "fr"
func TestBuildVerboseEmpty(t *testing.T) // no tokens: Words and Segments marshal as [] not null; Duration from AudioDurationMs
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test -race ./internal/soniox/ -run 'TestGroup|TestPick|TestBuild'`
Expected: FAIL (`undefined: groupSegments`)

- [ ] **Step 3: Implement `transcript.go`**

- [ ] **Step 4: Run to verify they pass**

Run: `go test -race ./internal/soniox/ -run 'TestGroup|TestPick|TestBuild'`
Expected: `ok  github.com/47vigen/remora/internal/soniox`

- [ ] **Step 5: Commit**

```bash
git add internal/soniox/transcript.go internal/soniox/transcript_test.go
git commit -m "feat(soniox): group tokens into words and speaker segments"
```

---

### Task 5: The adapter handler (upload, create, poll, fetch, always delete)

**Files:**
- Create: `internal/soniox/handler.go`, `internal/soniox/errors.go`
- Test: `internal/soniox/handler_test.go`

**Interfaces:**
- Consumes: Tasks 2–4.
- Produces:

```go
type Config struct {
    BaseURL      string        // default "https://api.soniox.com"
    PollInterval time.Duration // default 1s; clamped to [500ms, 60s]
    Deadline     time.Duration // default 30m; whole job from upload to transcript
    Grouping     Grouping      // defaults 1s / 30s
    HTTPClient   *http.Client  // default &http.Client{} (no overall timeout; contexts bound every call)
    Logger       *slog.Logger
}
type Adapter struct{ /* client *Client; cfg Config; after func(time.Duration) <-chan time.Time */ }
func New(cfg Config) *Adapter
func (a *Adapter) ServeHTTP(w http.ResponseWriter, r *http.Request)
func clampPoll(d time.Duration) time.Duration
func writeError(w http.ResponseWriter, status int, typ, msg string) // {"error":{"message":msg,"type":typ,"code":null}}
```

Flow of `ServeHTTP` (only `POST /v1/audio/transcriptions`; anything else → 404 `not_found`):

1. Key = bearer from `Authorization`; missing → 401 `authentication_error`.
2. `r.ParseMultipartForm(32 << 20)`, `defer r.MultipartForm.RemoveAll()`; `parseParams`; `ParamError` → 400 `invalid_request_error`; no `file` part → 400.
3. `ctx, cancel := context.WithTimeout(r.Context(), cfg.Deadline)`; `ref := RefPrefix + hex(16 random bytes)`.
4. Upload (filename from the part header), create with `FileID` and `ClientReferenceID = ref`.
5. **One deferred cleanup closure**, registered right after the upload succeeds; it reads `fileID` and `transcriptionID` (the latter set once create succeeds) and deletes the file first, then the transcription if one exists (two separate `defer`s would run LIFO and delete the transcription first). Each delete uses `context.WithTimeout(context.WithoutCancel(r.Context()), 30*time.Second)` each; a failure is logged at `warn` with ids and `redact`ed error, never returned (the sweeper is the backstop).
6. Poll: first `GetTranscription` immediately, then wait `min(clampPoll(cfg.PollInterval), time.Until(deadline))` via `a.after` between polls. `completed` → fetch transcript; `error` → 502 `upstream_error` with `"transcription failed: <error_type>: <error_message>"`; deadline reached → 504 `timeout` with `"transcription did not complete within <Deadline>"`; `r.Context()` cancelled → return without writing.
7. Respond 200: `json` → `{"text": ts.Text}`; `text` → `text/plain; charset=utf-8` body `ts.Text`; `verbose_json` → `buildVerbose`.

Error mapping (`errors.go`): `*APIError` with 401/402/403/429 → same status (so Bifrost's fallback sees 402/429), other 4xx → 400, 5xx and transport errors → 502. Types: 401 `authentication_error`, 402 `insufficient_quota`, 429 `rate_limit_exceeded`, else `upstream_error`. Messages are `redact`ed and say "Soniox …", never naming an engine. Log lines carry `ref`, `transcription_id`, `file_id`, status and duration; never the key or the audio.

- [ ] **Step 1: Write the failing tests** (`httptest` fake from Task 1 behind `New(Config{BaseURL: srv.URL, PollInterval: time.Second})`; tests replace `a.after` with a func that records the duration and returns a closed channel, so they run instantly)

```go
func postAudio(t *testing.T, h http.Handler, key string, fields map[string]string) *httptest.ResponseRecorder

func TestHandlerVerboseJSON(t *testing.T) {
    // script two speakers; fields model, response_format=verbose_json, enable_speaker_diarization=true
    // rec.Code == 200; v.Segments[0].Speaker == "1"; v.Segments[1].Speaker == "2"; v.Duration == 3.5; v.Language == "fa"
    // fake.State().Live is empty; len(fake.State().Deleted) == 2
    // create body: enable_speaker_diarization == true (JSON bool), client_reference_id has prefix "remora:"
}
func TestHandlerJSONAndText(t *testing.T) // json → {"text":...} only; text → Content-Type text/plain, body == script.Text
func TestHandlerForwardsContextAndHints(t *testing.T) // context={"terms":["X"]}, language_hints="fa,en" → create body has them verbatim
func TestHandlerPollIntervalClamped(t *testing.T) {
    // PollInterval 1ms, script ProcessingFor so 3 polls are "processing"
    // every recorded wait == 500*time.Millisecond; with PollInterval 5*time.Minute every wait == 60*time.Second
}
func TestHandlerDeadline(t *testing.T) // Deadline 50ms, real a.after, ProcessingFor 1h → 504, error.type == "timeout"; file deleted
func TestHandlerJobErrorCleansUp(t *testing.T) // FailWith "audio_decode_failed" → 502, message contains "audio_decode_failed"; Live empty
func TestHandlerCreateRejectedCleansUpFile(t *testing.T) // HTTPStatus 429 → 429 rate_limit_exceeded; uploaded file deleted
func TestHandlerCleansUpWhenClientCancels(t *testing.T) // cancel request ctx after first poll → handler returns; within 1s fake.State().Deleted == [file, transcription] in that order
func TestHandlerCleanupOrderOnSuccess(t *testing.T)     // completed job → Deleted[0] is the file id, Deleted[1] the transcription id
func TestHandlerParamErrorIs400(t *testing.T) // field foo=1 → 400, error.message == `unsupported parameter "foo"`; fake saw no upload
func TestHandlerMissingKeyIs401(t *testing.T)
func TestHandlerErrorRedactsKey(t *testing.T) // EchoAuthInError + key "sk-test-secret": body has no "sk-test-secret"
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test -race ./internal/soniox/ -run TestHandler`
Expected: FAIL (`undefined: New`)

- [ ] **Step 3: Implement `handler.go` and `errors.go`**

- [ ] **Step 4: Run the package**

Run: `go test -race ./internal/soniox/`
Expected: `ok  github.com/47vigen/remora/internal/soniox`

- [ ] **Step 5: Commit**

```bash
git add internal/soniox
git commit -m "feat(soniox): OpenAI transcription handler over the async API with guaranteed cleanup"
```

---

### Task 6: Sweeper for leftovers

**Files:**
- Create: `internal/soniox/sweep.go`
- Test: `internal/soniox/sweep_test.go`

**Interfaces:**
- Consumes: `Client`, `RefPrefix` (Task 2).
- Produces:

```go
type KeySource func(ctx context.Context) ([]string, error)
type Sweeper struct {
    Client *Client
    Keys   KeySource
    MaxAge time.Duration    // 1h, from the spec
    Now    func() time.Time // default time.Now
    Logger *slog.Logger
}
type SweepResult struct{ Transcriptions, Files int }
func (s *Sweeper) SweepOnce(ctx context.Context) (SweepResult, error) // joined errors of failed keys (errors.Join)
func (s *Sweeper) Run(ctx context.Context, every time.Duration)    // sweeps at once, then every `every`, until ctx is done
```

Per key, in this order:

1. `ListTranscriptions`. `active` = set of `FileID` whose status is `queued` or `processing`.
2. Delete each transcription with prefix `remora:`, `CreatedAt` before `Now()-MaxAge`, and status `completed` or `error`.
3. `ListFiles`. Delete each file with prefix `remora:`, older than `MaxAge`, and not in `active` (deleting it would fail a job still running).
4. An item that fails is logged and skipped; a key whose list fails is logged and joined into the returned error; other keys still run.

Objects without the prefix are never touched, whatever their age.

- [ ] **Step 1: Write the failing tests** (fake seeded with `Seed`; `Now` fixed at `t0`)

```go
func TestSweepDeletesOldOwned(t *testing.T) {
    // seed key "k": file remora:a (t0-2h), completed transcription remora:a (t0-2h) → both deleted
    // res == SweepResult{Transcriptions:1, Files:1}
}
func TestSweepKeepsYoungAndForeign(t *testing.T) // remora:b at t0-30m and "app:x" at t0-5h survive
func TestSweepKeepsActiveJobAndItsFile(t *testing.T) // processing remora:c at t0-3h and its file survive
func TestSweepEachKeyIndependently(t *testing.T) // keys {"bad","k"}; fake 401s "bad" → k still swept; err != nil
func TestSweepRunStopsOnCancel(t *testing.T) // Run with every=10ms; cancel; returns within 100ms; at least 1 sweep happened
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test -race ./internal/soniox/ -run TestSweep`
Expected: FAIL (`undefined: Sweeper`)

- [ ] **Step 3: Implement `sweep.go`**

- [ ] **Step 4: Run to verify they pass**

Run: `go test -race ./internal/soniox/ -run TestSweep`
Expected: `ok  github.com/47vigen/remora/internal/soniox`

- [ ] **Step 5: Commit**

```bash
git add internal/soniox/sweep.go internal/soniox/sweep_test.go
git commit -m "feat(soniox): hourly sweep of remora-tagged files and transcriptions"
```

---

### Task 7: Serve the adapter, run the sweeper, register `soniox` in Bifrost

**Files:**
- Modify: `internal/config/config.go` (+ its test), `cmd/remora/serve.go`
- Create: `internal/reconcile/soniox.go`
- Test: `internal/reconcile/soniox_test.go`, `internal/config/config_test.go`

**Interfaces:**
- Consumes: `soniox.New`, `soniox.Sweeper` (Tasks 5, 6); plan 02's `Store.ProviderKeysByKind`, `bifrost.Provider` and the provider mapping in `internal/reconcile/desired.go`.
- Produces:

```go
// internal/config: new fields, env names, defaults
SonioxAddr          string        // "127.0.0.1:" + Ports.Soniox (REMORA_PORT_SPEECH, default 18090)
SonioxBaseURL       string        // REMORA_SONIOX_BASE_URL, default "https://api.soniox.com"
SonioxPollInterval  time.Duration // REMORA_SONIOX_POLL_INTERVAL, default 1s
SonioxDeadline      time.Duration // REMORA_SONIOX_DEADLINE, default 30m
SonioxSweepInterval time.Duration // REMORA_SONIOX_SWEEP_INTERVAL, default 10m

// internal/reconcile: a store provider of kind "soniox" (it must be named "soniox") becomes this front-door provider
func sonioxProvider(adapterURL string, deadline time.Duration) bifrost.Provider // {Name "soniox", BaseType "openai", BaseURL adapterURL, AllowPrivateNetwork true, AllowedRequests ["transcription"], RequestTimeout deadline+60s}
const SonioxProviderName = "soniox" // the name clients use: "soniox/<model>"
```

`sonioxProvider("http://127.0.0.1:18090", 30*time.Minute)`, created through plan 02's `bifrost.Client.CreateProvider`, must send exactly this body (`stream_idle_timeout_in_seconds` and `concurrency_and_buffer_size` are what plan 02's client sends for every provider):

```json
{"provider":"soniox",
 "network_config":{"base_url":"http://127.0.0.1:18090","default_request_timeout_in_seconds":1860,"max_retries":0,"stream_idle_timeout_in_seconds":300,"allow_private_network":true},
 "concurrency_and_buffer_size":{"concurrency":1000,"buffer_size":5000},
 "custom_provider_config":{"base_provider_type":"openai","is_key_less":false,"allowed_requests":{"transcription":true}}}
```

The reconciler learns the adapter through two fields, set alike by `serve` and `apply` (`newReconciler` in `cmd/remora`): `SonioxURL` (`"http://" + cfg.SonioxAddr`; empty makes a `soniox` provider `ErrInvalid`) and `SonioxDeadline` (0 = 30m). A store provider of kind `soniox` takes no `type` and no `base_url`: its upstream is `REMORA_SONIOX_BASE_URL`.

(`base_url` has no `/v1`: Bifrost appends `/v1/audio/transcriptions`. `allowed_requests` with only `transcription` disables list-models, chat and streaming for this provider.) The provider's keys follow plan 02's key path unchanged.

`serve.go`: before supervising engines, start `&http.Server{Addr: cfg.SonioxAddr, Handler: soniox.New(...), ReadHeaderTimeout: 10 * time.Second}` (no write timeout: a response may take the whole deadline) and `go (&soniox.Sweeper{Keys: func(ctx) { return st.ProviderKeysByKind(ctx, store.ProviderKind("soniox")) }, MaxAge: time.Hour, ...}).Run(ctx, cfg.SonioxSweepInterval)`; shut the server down with the others.

- [ ] **Step 1: Write the failing tests**

```go
func TestSonioxProviderBody(t *testing.T) // CreateProvider(sonioxProvider(...)) against an httptest front door: JSONEq against the body above
func TestSonioxProviderTimeoutFollowsDeadline(t *testing.T) // deadline 2h → RequestTimeout 7260s, sent as default_request_timeout_in_seconds == 7260
func TestSonioxKindNameMustBeSoniox(t *testing.T) // store provider kind soniox named "speech" → reconcile ErrInvalid
func TestConfigSonioxDefaults(t *testing.T) // empty env → SonioxAddr "127.0.0.1:18090", PollInterval 1s, Deadline 30m, SweepInterval 10m
func TestConfigSonioxOverrides(t *testing.T) // REMORA_PORT_SPEECH=19000, REMORA_SONIOX_DEADLINE=1h → "127.0.0.1:19000", time.Hour
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test -race ./internal/reconcile/ ./internal/config/ -run 'Soniox'`
Expected: FAIL (`undefined: sonioxProvider`)

- [ ] **Step 3: Implement the config fields, `sonioxProvider`, the `soniox` case in plan 02's `desired.go`, and the wiring in `serve.go`**

- [ ] **Step 4: Run all unit tests and build**

Run: `go test -race ./... && go build ./cmd/remora`
Expected: every package `ok`, build exits 0

- [ ] **Step 5: Commit**

```bash
git add internal/config internal/reconcile cmd/remora
git commit -m "feat(soniox): serve the adapter, sweep leftovers, register the soniox provider"
```

---

### Task 8: The `speaker` patch to Bifrost (fork, upstream PR, submodule, docs)

**Files:**
- Modify (in the fork): `core/schemas/transcriptions.go:213-224`, `core/schemas/transcriptions_test.go`
- Modify (remora): `.gitmodules`, `engines/bifrost` (submodule pointer), `docs/patches.md` (create if absent)

**Interfaces:**
- Produces: `schemas.TranscriptionSegment.Speaker *string \`json:"speaker,omitempty"\`` in the pinned engine (pointer + omitempty, the same as `TranscriptionWord.Speaker` at `transcriptions.go:209`, so non-diarized output is unchanged).

- [ ] **Step 1: Branch the fork from the pinned commit**

```bash
PIN=$(git -C engines/bifrost rev-parse HEAD)
git -C engines/bifrost remote add fork https://github.com/47vigen/bifrost.git   # fork created on GitHub first if missing
git -C engines/bifrost switch -c remora/transcription-segment-speaker "$PIN"
```

- [ ] **Step 2: Write the failing test** in `engines/bifrost/core/schemas/transcriptions_test.go`

```go
func TestTranscriptionSegment_SpeakerRoundTrip(t *testing.T) {
    in := `{"text":"a b","segments":[{"id":0,"start":0,"end":1,"text":"a","speaker":"1"},{"id":1,"start":1,"end":2,"text":"b"}]}`
    var r BifrostTranscriptionResponse
    // json.Unmarshal(in) ok; r.Segments[0].Speaker != nil && *r.Segments[0].Speaker == "1"; r.Segments[1].Speaker == nil
    // out, _ := json.Marshal(r): strings.Contains(out, `"speaker":"1"`); strings.Count(out, `"speaker"`) == 1
    // r.DiarizedSegments == nil (the sniff still picks verbose for int ids)
}
```

Run: `cd engines/bifrost/core && go test ./schemas -run TestTranscriptionSegment_SpeakerRoundTrip`
Expected: FAIL (`r.Segments[0].Speaker undefined`)

- [ ] **Step 3: Add the field** after `Text` in `TranscriptionSegment`, with the one-line comment `// Speaker label when the provider diarizes verbose_json segments`.

- [ ] **Step 4: Run the schemas and OpenAI provider tests**

Run: `cd engines/bifrost/core && go test ./schemas ./providers/openai/...`
Expected: `ok` for both

- [ ] **Step 5: Commit and push to the fork**

```bash
git -C engines/bifrost commit -am "feat(schemas): carry speaker on verbose transcription segments"
git -C engines/bifrost push fork remora/transcription-segment-speaker
```

- [ ] **Step 6: Open the upstream PR** (ask the owner before this outward step). Base = Bifrost's default branch (`gh repo view maximhq/bifrost --json defaultBranchRef -q .defaultBranchRef.name`; `dev` at the time of research). Title as the commit; body: the problem (verbose_json segments from a diarizing provider lose `speaker`; `words[].speaker` already exists), the one-field change, the test, no behaviour change when absent. Record the PR URL.

- [ ] **Step 7: Point the submodule at the fork**

```bash
git config -f .gitmodules submodule.engines/bifrost.url https://github.com/47vigen/bifrost.git
git submodule sync engines/bifrost
git -C engines/bifrost checkout remora/transcription-segment-speaker
```

- [ ] **Step 8: Write the `docs/patches.md` entry**

One Markdown table with the columns plan 07's bump tool reads, in this order:
`| engine | file | change | why | fork commit | upstream PR | base tag | remove when |`. One row: engine `bifrost`;
file `core/schemas/transcriptions.go`; change "add `speaker` to `TranscriptionSegment`"; why "Soniox verbose_json
segments carry speakers"; fork commit SHA; upstream PR URL (full `https://github.com/maximhq/bifrost/pull/<n>`);
base tag = the upstream release tag the fork branch starts from (`BIFROST_VERSION` in `engines/versions.env`, e.g.
`transports/v2.3.0`); remove when "the PR is in a release tag". One line above the table: an engine on a fork is
never bumped or rebased automatically; when a release contains the upstream fix, the bump workflow (plan 07) opens a
pull request that points the submodule back at upstream at that tag and removes the row.

- [ ] **Step 9: Verify the image still builds**

Run: `docker build -t remora:dev .`
Expected: exits 0

- [ ] **Step 10: Commit**

```bash
git add .gitmodules engines/bifrost docs/patches.md
git commit -m "build(engines): pin bifrost to fork with transcription segment speaker"
```

---

### Task 9: End-to-end through remora with the Soniox fake

**Files:**
- Create: `test/e2e/transcription_test.go` (`//go:build e2e`), `test/e2e/testdata/two-speakers.json` (a `Script` with Persian tokens containing U+200C and two speakers)
- Modify: plan 02's `test/e2e/testdata/remora.yaml`, which `TestMain` applies once (add provider `{name: soniox, kind: soniox, keys: [{name: main, value: env:E2E_SONIOX_KEY}]}` and key `{name: speech, value: env:E2E_KEY_SPEECH, allow: ["soniox/*"]}`; `apply` prunes what the file omits, so a second file would delete plan 02's fixtures)
- Modify: `test/e2e/compose.yaml` (remora env `E2E_SONIOX_KEY=fake-soniox-key`, `E2E_KEY_SPEECH`, `REMORA_SONIOX_BASE_URL=http://fakes:9090/u/sx`, `REMORA_SONIOX_POLL_INTERVAL=500ms`, `REMORA_SONIOX_SWEEP_INTERVAL=2s`)

**Interfaces:**
- Consumes: plan 01's e2e helpers and plan 02's `apply -f` seeding (names above), the fake control API (Task 1), the provider `soniox` (Task 7), the patched engine (Task 8).

Setup in each test: `POST fakesURL("/_control/soniox/reset")` and `PUT fakesURL("/_control/soniox/script")`. Requests go to `remoraURL(t, "/v1/audio/transcriptions")` with the `E2E_KEY_SPEECH` value, `model=soniox/stt-async-v5` and a small WAV from `testdata`.

- [ ] **Step 1: Write the tests**

```go
func TestTranscriptionSpeakersSurvive(t *testing.T) {
    // fields: response_format=verbose_json, enable_speaker_diarization=true, language=fa,
    //         context={"terms":["remora"]}, language_hints=fa,en
    // resp.StatusCode == 200
    // v.Segments: len 2; v.Segments[0].ID == 0, Speaker "1"; v.Segments[1].ID == 1, Speaker "2"
    // bytes.Equal([]byte(v.Segments[0].Text), []byte(script segment 0 text with U+200C))
    // every v.Words[i].Speaker != ""
    // state := GET /_control/soniox/state:
    //   len(state.Live) == 0; len(state.Deleted) == 2 (one file, one transcription, same client_reference_id prefix "remora:")
    //   create body: enable_speaker_diarization == true (bool), context == {"terms":["remora"]}, language_hints == ["fa","en"]
    //   the transcription's Key == "fake-soniox-key" (Bifrost forwarded the provider key)
}
func TestTranscriptionLongJob(t *testing.T) {
    // script ProcessingFor 130s; client timeout 5m
    // 200; elapsed >= 130s; state.Live empty; state.Polls >= 200
}
func TestTranscriptionJobErrorCleansUp(t *testing.T) // FailWith → non-2xx OpenAI error; body has no "bifrost"; state.Live empty
func TestTranscriptionUnknownParam(t *testing.T) // foo=1 → 400, message contains `unsupported parameter "foo"`; fake saw no upload
func TestTranscriptionSweep(t *testing.T) {
    // seed for key "fake-soniox-key": completed transcription + its file "remora:old" at now-2h; file "remora:new" at now-10m;
    //   file "other:old" at now-5h
    // within 10s (poll state every 500ms): "remora:old" objects deleted; "remora:new" and "other:old" still live
}
```

- [ ] **Step 2: Run them**

Run: `go test -tags e2e ./test/e2e/ -run TestTranscription -timeout 15m -v`
Expected: `--- PASS` for all five; `ok  github.com/47vigen/remora/test/e2e`

- [ ] **Step 3: Prove the patch matters** — temporarily check out `$PIN` in `engines/bifrost`, rebuild the image, rerun `TestTranscriptionSpeakersSurvive`.
Expected: FAIL on `Speaker "1"` (speaker missing). Restore the fork branch and rebuild.

- [ ] **Step 4: Commit**

```bash
git add test/e2e
git commit -m "test(e2e): transcription speakers, cleanup, long job and sweep through remora"
```

---

## Self-review notes

- Spec coverage: adapter and port (Tasks 5, 7), parameter pass-through and booleans (3), bounded poll and deadline (5), delete in a finally path (5), `verbose_json` with speaker segments (4), sweep (6), custom provider registration (7), upstream patch and `docs/patches.md` (8), the spec's e2e item "transcription through the Soniox fake with segments, speakers and deletion verified" (9).
- Not covered here, owned elsewhere: remora's `/v1` proxy has no timeout shorter than `SonioxDeadline + 60s` (plan 02, `TestProxy_NoResponseTimeout`); a forked engine is never bumped automatically and returns to upstream through plan 07's bump workflow; `soniox` as a provider kind in the OpenAPI contract and UI (plans 05, 06).

## Corrections found during implementation

- **The image must build against the submodule's core.** `transports/go.mod` requires the published
  `github.com/maximhq/bifrost/core` module, so a patch in the submodule's `core/` never reached the binary. The
  Dockerfile now copies `engines/bifrost/core` and builds with `go mod edit -replace github.com/maximhq/bifrost/core=../core`.
  At `transports/v2.3.0` the submodule's `core/` is byte-identical to `core/v1.12.0`, the version transports requires,
  so the only difference is the patch.
- **The upstream PR is the owner's call** (Task 8 Step 6). Until it is opened, the `upstream PR` cell of
  `docs/patches.md` reads `pending`. The bump tool accepts `pending` and holds the engine on its fork, and
  `scripts/check-engine-pins.sh` checks a patched engine against its row: the submodule at the fork commit, and the
  row's base tag equal to `engines/versions.env`.
- `.gitmodules` points `engines/bifrost` at the fork without a `branch` line; the gitlink pins the commit.
