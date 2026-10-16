# Hot Path in Code Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Cut remora's own CPU, garbage and held memory per `/v1` request without changing a single byte a client or the front door sees, apart from removing the engine details that leak today.

**Architecture:** The store keeps an `atomic.Pointer` snapshot of client keys and aliases. It is rebuilt at the end of every `Store.Write`, so no request touches SQLite. The proxy reads a request body once: `json.Valid`, then one scan of the top-level object that records byte spans. The model swap splices at a span, and the session hash reads a span. Bodies are released when `modifyResponse` returns. The SSE scrubber hands back every complete buffered line in one `Read`. Header values skip the regex unless a cheap check says an engine word may be present. Front-door `extra_fields` and `X-Cpa-*` headers are stripped on the way out.

**Tech Stack:** Go 1.27, `net/http/httputil`, `encoding/json` (no `jsontext`: it is still behind `GOEXPERIMENT=jsonv2`), `sync/atomic`, `testing` benchmarks and fuzzing, `golang.org/x/perf/cmd/benchstat`, Docker Compose e2e.

**Spec:** `docs/specs/2026-10-16-performance-design.md`, section "PR 2: Hot path in code". Evidence: lens 1 (hot path) and lens 4 (runtime) audits, summarised in the spec's Evidence section. Plan 08 (PR 1) lands first. It provides `tools/loadgen`, `make bench`, the SQLite idle pool, the transport idle pool of 1024, the server `IdleTimeout`, opt-in pprof and the filtered cooling tail. This plan repeats none of them.

Branch: `perf-hot-path`, one pull request.

## Global Constraints

- **Byte-for-byte behaviour.** Every existing unit and e2e test stays green and unedited, with one exception: `TestProxy_AliasesUnreadableFailsClosed`, which edits the DB behind the store's back and must now call `Refresh` (Task 2). The forwarded body, the `X-Bf-Eh-X-Opencode-Session` value and every refusal stay exactly what they are today. PR 3 changes the session key, not this plan.
- **Fail closed stays.** When the snapshot cannot be built, reads fall back to today's SQL path. That path returns the error, so a request is refused, never sent unchecked.
- **The scanner is at a trust boundary.** It never panics and never reads out of bounds on any input. A fuzz test proves it.
- **Never edit `engines/`.** Never leak engine names, keys or ids, as `AGENTS.md` requires.
- Each optimisation gets a `Benchmark*` in its package's `_test.go`, with `b.ReportAllocs()`. Before and after numbers come from the same benchmark, run on the base commit and on the branch head.
- Benchmark and profile output is never committed. Keep it under `$TMPDIR`.
- Code style: `log/slog`, errors wrapped with `%w`, comments only for the why. Commits: Conventional Commits with scopes.

## Review Focus

1. **A key revoked or deleted through the API is refused on the very next request.** The snapshot is rebuilt at commit, and concurrent writes never leave a stale snapshot behind. Tests: Task 2, `TestSnapshotSeesWriteAtCommit` and `TestSnapshotConcurrentWrites`.
2. **Every body the old parser refused is still refused, with the same error.** That covers `"Model"`, `"model"`, duplicate models, `"Fallbacks"` and a model that is not a string. Every accepted body yields the same model. Test: Task 3, `FuzzScanBodyParity`.
3. **The derived OpenCode session id is identical to today's for every body.** Turns of one conversation must keep one id. Tests: Task 4, `FuzzFirstMessageParity` and `TestOpencodeSessionGolden`.
4. **The batched SSE scrubber still rewrites an error event that shares a read with content.** It never blocks waiting for more bytes while a complete line is buffered. Test: Task 6, `TestSSEScrubberBatchesWithoutBlocking`.
5. **Only the top-level `extra_fields` is removed.** A message, tool argument or nested object containing `"extra_fields"` survives byte for byte, Persian ZWNJ included. Tests: Task 8, `TestStripExtraFieldsTopLevelOnly` and e2e `TestNoEngineDetailInResponses`.

## File map

```
internal/store/snapshot.go          snapshot type, Refresh, Alias (Task 2)
internal/store/clientkeys.go        ClientKeyByValue reads the snapshot (Task 2)
internal/store/aliases.go           Aliases reads the snapshot (Task 2)
internal/store/tx.go, secret.go     Write and SetSealer call Refresh (Task 2)
internal/store/bench_test.go        BenchmarkKeyLookup, BenchmarkAliases, BenchmarkPerRequestParallel (Task 1)
internal/proxy/scan.go              scanObject, skipValue, scanBody, element helpers (Task 3)
internal/proxy/extract.go           extractModel returns spans, presized read, splice (Task 3)
internal/proxy/session.go           firstMessage from spans (Task 4)
internal/proxy/proxy.go             reqInfo.spans, body release, Alias lookup (Tasks 2–5)
internal/proxy/response.go          SSE batching, header fast path, extra_fields, X-Cpa-* (Tasks 6–8)
internal/proxy/bench_test.go        ported lens-1 benchmarks (Task 1)
test/e2e/leak_test.go               TestNoEngineDetailInResponses (Task 8)
```

---

### Task 1: Benchmarks on the base commit

**Files:**
- Create: `internal/proxy/bench_test.go`, `internal/store/bench_test.go`

**Interfaces:**
- Consumes: today's `extractModel`, `withModel`, `opencodeSession`, `newSSEScrubber`, `setRemoraHeaders`, `scrubString`, `Store.ClientKeyByValue` and `Store.Aliases`.
- Produces: the benchmarks every later task re-runs. Port them from the lens-1 audit's `proxy_bench_test.go` and `store_bench_test.go`. Keep the fixture builders `benchChatBody(msgs int)`, `sseStream(n int)` and `benchStore(b)`, and the sizes 2, 100 and 500 messages, which give about 4 KB, 208 KB and 1 MB.

```go
// internal/proxy/bench_test.go
func BenchmarkRequestPath(b *testing.B)       // per size: extractModel + withModel + opencodeSession, the whole per-request body cost
func BenchmarkExtractModel(b *testing.B)
func BenchmarkWithModel(b *testing.B)
func BenchmarkOpencodeSession(b *testing.B)
func BenchmarkSSEScrubber(b *testing.B)       // 1000 events; ReportMetric reads/event and allocs
func BenchmarkCleanResponseHeaders(b *testing.B)
// internal/store/bench_test.go
func BenchmarkKeyLookup(b *testing.B)
func BenchmarkAliases(b *testing.B)
func BenchmarkPerRequestParallel(b *testing.B) // b.RunParallel: ClientKeyByValue + Aliases
```

Later tasks change only the call sites. When a signature changes, edit the benchmark in the same commit and keep its name, so benchstat pairs the old and new rows.

- [ ] **Step 1: Add the benchmarks.** Leave the code they measure unchanged.
- [ ] **Step 2: Record the baseline.**
  Run: `go test -run '^$' -bench . -benchmem -count 6 ./internal/proxy ./internal/store | tee $TMPDIR/remora-bench-before.txt`
  Expected: the numbers are in the lens-1 ranges: about 6.3 ms and 15 MB per 1 MB request, 2 reads per event, 81 allocs per header clean, and 78 µs per key lookup plus alias scan.
- [ ] **Step 3: Commit**

```bash
git add internal/proxy/bench_test.go internal/store/bench_test.go
git commit -m "test(proxy,store): benchmarks for the /v1 hot path"
```

---

### Task 2: In-memory snapshot of keys and aliases

**Files:**
- Create: `internal/store/snapshot.go`, `internal/store/snapshot_test.go`
- Modify: `internal/store/{store.go,tx.go,secret.go,clientkeys.go,aliases.go}`, `internal/proxy/proxy.go` (`checkAliasKind`), `internal/proxy/proxy_test.go` (`TestProxy_AliasesUnreadableFailsClosed` only)

**Writers, verified.** These are every statement that changes `client_keys`, `aliases` or `alias_members`, from a grep for `INSERT|UPDATE|DELETE` against those tables outside `engines/`. All of them are `*Tx` methods, and a `*Tx` exists only inside `Store.Write`:

| method | file |
|---|---|
| `PutClientKey`, `SetClientKeyVK`, `DeleteClientKey` | `internal/store/clientkeys.go:120,165,185` |
| `PutAlias` (also replaces `alias_members`), `DeleteAlias` (members cascade), `SetAliasRuleID` | `internal/store/aliases.go:76,105,114` |

`Store.Write` has three callers: `internal/api/adapters.go:62`, which backs every API handler including `remora apply` and `import` (both use the HTTP API through `cmd/remora/client.go`), and `internal/reconcile/reconcile.go:121,142`, the pass and `Change`. Only `cmd/remora/serve.go:68` opens the store, so no other process writes. The one raw writer is a test, `proxy_test.go:871`.

**Interfaces:**
- Consumes: `Sealer.MAC` and `Sealer.Open`, `scanClientKey`, `loadAliases`.
- Produces:

```go
// snapshot.go
type snapshot struct {
    keys    map[string]ClientKey   // by string(Sealer.MAC(value)); VKValue already opened
    aliases map[string]alias.Alias // members in order; shared, read-only
}
// Refresh rebuilds the snapshot from the committed DB under s.snapMu, so the last rebuild to run reads the last
// commit. On error it stores nil (reads fall back to SQL and fail closed there) and returns the error. With no
// sealer it stores nil and returns nil.
func (s *Store) Refresh(ctx context.Context) error
// Alias is the one alias a request needs, without cloning the map.
func (s *Store) Alias(ctx context.Context, name string) (alias.Alias, bool, error)

// Store gains: snap atomic.Pointer[snapshot]; snapMu sync.Mutex
// Write: after fn and the commit or rollback, it calls s.Refresh(context.WithoutCancel(ctx)) and ignores the
// error, which Refresh already handles. Every reconcile pass runs inside Write, so each pass also re-reads the
// DB, which is the spec's safety net for hand edits.
// SetSealer: after Store, it calls Refresh(context.Background()).
// ClientKeyByValue: with a snapshot, it is one map lookup; Enabled is still checked by the caller. With none,
// it uses today's SQL. Returned Allow slices are shared; callers only read them.
// Aliases: returns maps.Clone of the snapshot (API callers may mutate the map), or SQL when there is none.
```

`checkAliasKind` calls `p.store.Alias` instead of `p.store.Aliases`.

- [ ] **Step 1: Write the failing tests**

```go
func TestSnapshotSeesWriteAtCommit(t *testing.T)
// Put key k (enabled) and alias a; ClientKeyByValue(v).Enabled == true; Write: PutClientKey(enabled=false) →
// immediately ClientKeyByValue(v).Enabled == false; DeleteClientKey → errors.Is(err, ErrNotFound);
// DeleteAlias(a) → Alias(a) ok == false
func TestSnapshotRolledBackWriteChangesNothing(t *testing.T) // fn returns error after PutAlias → Alias(name) ok == false
func TestSnapshotConcurrentWrites(t *testing.T)
// 16 goroutines × 20 Writes each PutAlias(unique name); after Wait: Aliases() has all 320, and reflect.DeepEqual with tx.Aliases inside a fresh Write
func TestSnapshotLookupHitsNoSQL(t *testing.T)
// after a Write, raw s.DB().Exec("ALTER TABLE client_keys RENAME TO ck_gone") and the same for aliases;
// ClientKeyByValue(v) and Alias(a) still answer from the snapshot
func TestRefreshFailureFallsBackToSQL(t *testing.T)
// raw UPDATE aliases SET capabilities='not a list'; Refresh(ctx) != nil; Aliases(ctx) returns an error (fail closed)
func TestAliasesReturnsACopy(t *testing.T) // delete a key from the returned map; Aliases() again still has it
```

In `proxy_test.go`, `TestProxy_AliasesUnreadableFailsClosed` gets one new line after its raw `UPDATE`: `_ = f.store.Refresh(t.Context())`, through whatever field the fixture holds its store in. Its assertions do not change.

- [ ] **Step 2: Run them and confirm they fail.** Run `go test -race ./internal/store -run 'Snapshot|Refresh|AliasesReturns'`. Expected: FAIL (undefined: `Refresh`, `Alias`).
- [ ] **Step 3: Implement** `snapshot.go` and the wiring above. `refresh` reuses `scanClientKey` over `SELECT clientKeyCols, key_mac FROM client_keys` and `loadAliases(ctx, s.db)`.
- [ ] **Step 4: Run them and confirm they pass.** Run `go test -race ./internal/store ./internal/proxy ./internal/api ./internal/reconcile`. Expected: `ok`.
- [ ] **Step 5: Benchmark.** Run `go test -run '^$' -bench 'KeyLookup|Aliases|PerRequest' -benchmem -count 6 ./internal/store`. Expected: the key lookup is under 1 µs (lens 1 measured 164 ns), with 0 SQL; `BenchmarkPerRequestParallel` is at least 10× faster than the baseline.
- [ ] **Step 6: Commit**

```bash
git add internal/store internal/proxy/proxy.go internal/proxy/proxy_test.go
git commit -m "perf(store): serve client keys and aliases from a snapshot rebuilt on every write"
```

---

### Task 3: One pass over the body

**Files:**
- Create: `internal/proxy/scan.go`, `internal/proxy/scan_test.go`
- Modify: `internal/proxy/extract.go`, `internal/proxy/proxy.go` (`forward`, `reqInfo`), `internal/proxy/extract_test.go` (only the `withModel` call signature), `internal/proxy/bench_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces (Task 4 and plan 10, PR 3, consume `bodySpans`):

```go
type span struct{ start, end int } // raw[start:end] is one JSON value; end == 0 means absent

type bodySpans struct {
    model     span // the single top-level "model" value
    messages  span // the last top-level key strings.EqualFold "messages" (encoding/json's struct rule)
    input     span // the same for "input"
    firstUser span // the first element of messages, else of an input array, that is an object whose top-level
                   // "role" is the string "user"; PR 3 hashes it. Absent when there is none.
}

// scanObject calls fn(key, value) for each top-level member of the object in b, in order, until fn returns
// false. key is the decoded key; it is decoded with json.Unmarshal only when it contains a backslash, otherwise
// it aliases b. ok is false when b is not one well-formed object. It never panics and never reads past len(b).
func scanObject(b []byte, fn func(key []byte, v span) bool) (ok bool)
func skipValue(b []byte, i int) (end int, ok bool)
func forEachElement(b []byte, arr span, fn func(v span) bool) (ok bool) // arr must start with '['

// scanBody replaces jsonModel's walk and has the same refusals. json.Valid runs first: the front door's parser
// must never see anything the scanner did not.
func scanBody(raw []byte) (model string, sp bodySpans, err error)
func jsonModel(raw []byte) (string, error) // kept for ws.go:202; returns scanBody's model and err

func extractModel(r *http.Request) (model string, raw []byte, sp bodySpans, err error) // multipart → zero spans
func withModel(body []byte, sp bodySpans, contentType, model string) ([]byte, error)    // JSON: splice at sp.model
```

Scanner sketch. It follows the lens-1 prototype `scanTop`, plus bounds checks:

```
skipWS; expect '{'; loop: skipWS; '}' → done; expect '"'; key end = skipString (find next unescaped '"',
  fail at len(b)); expect ':'; v.start = skipWS; v.end = skipValue; fn(key, v); skipWS; ',' → continue, '}' → done.
skipString: bytes.IndexAny(b[i:], `"\`); -1 → fail; '\' → i += 2 (fail if past len), continue.
skipValue: '"' → skipString; '{' or '[' → depth counter over the bytes, skipString on every '"', fail at
  len(b) before depth 0; otherwise advance to the first of ",}] \t\r\n" or len(b).
```

`scanBody` applies `jsonModel`'s rules in key order. A key `EqualFold` to `fallbacks` returns `ErrClientFallbacks` at once. For `model`, it counts every `EqualFold` match, flags a key that is not spelled exactly `model`, and decodes the value with `json.Unmarshal` into a string, which flags a non-string. After the walk come the same `modelCount > 1`, spelling, non-string and empty checks, in that order. `withModel` keeps `json.Marshal(model)` for the new value, so the escaping is exactly today's.

Presize the read. When `0 < r.ContentLength <= MaxBodyBytes`, read into a buffer of capacity `ContentLength + bytes.MinRead`, so `bytes.Buffer.ReadFrom`'s last EOF probe does not double the buffer. Keep the `MaxBodyBytes+1` limit and both too-large paths.

- [ ] **Step 1: Write the failing tests**

```go
func TestScanObjectSpans(t *testing.T)
// `{"a":1, "b" : "x\"y" ,"c":{"d":[1,{"e":"]"}]},"f":null}` → keys a,b,c,f; raw[v] == `1`, `"x\"y"`, `{"d":[1,{"e":"]"}]}`, `null`
func TestScanObjectEscapedKey(t *testing.T)   // `{"model":"m"}` → key "model"
func TestScanObjectRejects(t *testing.T)      // table: ``, `[]`, `{`, `{"a"}`, `{"a":}`, `{"a":"x`, `{"a":1,}` (scanner alone, no json.Valid) → ok == false, no panic
func FuzzScanObject(f *testing.F)             // any bytes: no panic; when json.Valid and the top level is an object, ok == true and every span is inside b and itself json.Valid
func FuzzScanBodyParity(f *testing.F)
// seeds: every TestExtractModel JSON row plus `{"model":"m"}`, `{"fallbacks":1,"model":"m"}`, `{"model":1}`
// any bytes: (model, errClass) of scanBody == of jsonModelRef, the old jsonModel moved verbatim into scan_test.go;
// errClass is ErrClientFallbacks | ErrNoModel | nil, via errors.Is.
// One accepted divergence: input that is not valid JSON but carries a fallbacks key before the break. The old
// walk returned ErrClientFallbacks, and json.Valid-first returns ErrNoModel. Both are 400 refusals, so the fuzz
// test skips the case when !json.Valid(raw).
func TestBodySpansFirstUser(t *testing.T)
// messages [system, developer, user "u1", user "u2"] → raw[firstUser] == the u1 object; input array likewise;
// no user message → absent; "role":"USER" → absent (exact match)
func TestWithModelSplice(t *testing.T)        // FuzzScanBodyParity-accepted bodies: withModel(new) == jsonWithModelRef(old) byte for byte; Persian ZWNJ fixture included
func TestExtractModelPresized(t *testing.T)   // ContentLength = len(body): testing.AllocsPerRun of the read ≤ 2; ContentLength = -1 still works; ContentLength > MaxBodyBytes → ErrBodyTooLarge
```

- [ ] **Step 2: Run them and confirm they fail.** Run `go test -race ./internal/proxy -run 'Scan|BodySpans|WithModelSplice|Presized'`. Expected: FAIL (undefined: `scanObject`, …).
- [ ] **Step 3: Implement** `scan.go`, rewrite `jsonModel` and `jsonWithModel` over it, and thread `sp` through `extractModel`, `reqInfo.spans` and `forward`.
- [ ] **Step 4: Run them and confirm they pass.** Run `go test -race ./internal/proxy && go test ./internal/proxy -run '^$' -fuzz FuzzScanBodyParity -fuzztime 60s && go test ./internal/proxy -run '^$' -fuzz FuzzScanObject -fuzztime 60s`. Expected: `ok`, and no new corpus failures.
- [ ] **Step 5: Benchmark.** Run `go test -run '^$' -bench 'ExtractModel|WithModel' -benchmem -count 6 ./internal/proxy`. Expected at 1 MB: `ExtractModel` at about 1.6 ms, down from 2.3 ms, and under 1.1 MB allocated (the presized buffer only), down from 7.5 MB. `WithModel` allocates one body-sized slice and does no parse.
- [ ] **Step 6: Commit**

```bash
git add internal/proxy
git commit -m "perf(proxy): read the request body in one top-level scan and splice the model"
```

---

### Task 4: Session hash from the spans

**Files:**
- Modify: `internal/proxy/session.go`, `internal/proxy/session_test.go`, `internal/proxy/proxy.go` (`rewrite`)

**Interfaces:**
- Consumes: `bodySpans`, `forEachElement`.
- Produces:

```go
func opencodeSession(h http.Header, body []byte, sp bodySpans) string // same output as today for every body
func firstMessage(body []byte, sp bodySpans) []byte                    // aliases body; no copy
```

The old `firstMessage` unmarshals into `struct{ Messages []json.RawMessage; Input json.RawMessage }`. The new one must reproduce that exactly:
- When `messages` is present and is neither an array nor `null`, the old unmarshal fails, so the result is nil, even when `input` is valid.
- A non-empty `messages` array gives its first element.
- Otherwise, a non-empty `input` array gives its first element.
- Otherwise, an `input` string gives the whole string value, quotes included.
- Otherwise the result is nil.

Hash `"opencode\x00ses_\x00"` and the slice through one `sha256.New()` with two `Write` calls, which avoids the `append` copy.

- [ ] **Step 1: Write the failing tests**

```go
func TestOpencodeSessionGolden(t *testing.T)
// table of 8 bodies → exact ids, the expected strings produced by running today's opencodeSession on the base
// commit (generate them once, paste them as literals): chat with system first, chat with Persian ZWNJ, input
// array, input string, "Messages" capitalised, duplicate messages keys, messages:null + input array,
// messages:{} + input array (→ random: assert format only, and that two calls differ)
func FuzzFirstMessageParity(f *testing.F)
// any json.Valid object: bytes.Equal(firstMessage(raw, spans), firstMessageRef(raw)), where firstMessageRef is
// the old function moved verbatim into session_test.go
```

The existing `TestOpencodeSession` changes only its call signature.

- [ ] **Step 2: Run them and confirm they fail.** Run `go test -race ./internal/proxy -run 'OpencodeSession|FirstMessage'`. Expected: FAIL (too many arguments in the call).
- [ ] **Step 3: Implement.** `rewrite` passes `info.spans`.
- [ ] **Step 4: Run them and confirm they pass.** Run `go test -race ./internal/proxy && go test ./internal/proxy -run '^$' -fuzz FuzzFirstMessageParity -fuzztime 60s`. Expected: `ok`.
- [ ] **Step 5: Benchmark.** Run `go test -run '^$' -bench 'OpencodeSession|RequestPath' -benchmem -count 6 ./internal/proxy`. Expected: `OpencodeSession` at 1 MB costs about 1 µs plus the hash of the first message, with 0–2 allocs, down from 1.46 ms and 1.2 MB. `RequestPath` at 1 MB is at least 3× faster, and allocates more than 10× less, than the baseline's 6.35 ms and 15 MB.
- [ ] **Step 6: Commit**

```bash
git add internal/proxy
git commit -m "perf(proxy): derive the OpenCode session from the scanned spans"
```

---

### Task 5: Release request bodies early

**Files:**
- Modify: `internal/proxy/proxy.go`, `internal/proxy/proxy_test.go`

**Interfaces:**
- Produces:

```go
// dropReader serves b once and lets go of it at EOF or Close.
type dropReader struct{ b []byte }
func (d *dropReader) Read(p []byte) (int, error)
func (d *dropReader) Close() error
// forward: out.Body = &dropReader{b: body} (it was io.NopCloser(bytes.NewReader(body)))
// modifyResponse: after setDroppedParams, info.body = nil and info.spans = bodySpans{}
// errorHandler: info.body = nil when info != nil
```

- [ ] **Step 1: Write the failing tests**

```go
func TestDropReaderReleasesAtEOF(t *testing.T)     // read all → d.b == nil; Close before EOF → d.b == nil; a Read after Close → 0, io.EOF
func TestModifyResponseReleasesBody(t *testing.T)
// hook captures reqBody (len > 0 inside the hook, so the hooks still see it); after the response the captured
// *reqInfo has body == nil; the upstream received the exact body
func TestErrorHandlerReleasesBody(t *testing.T)    // upstream unreachable → info.body == nil
```

- [ ] **Step 2: Run them and confirm they fail.** Run `go test -race ./internal/proxy -run 'DropReader|ReleasesBody'`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run them and confirm they pass.** Run `go test -race ./internal/proxy`. Expected: `ok`, with the hook and dropped-params tests unchanged and green.
- [ ] **Step 5: Benchmark.** Add `BenchmarkHeldBodies` to `bench_test.go`. It runs 200 concurrent requests with 1 MB bodies against an `httptest` upstream that holds each stream open. Inside, it calls `runtime.GC()` and reports `runtime.MemStats.HeapInuse` with `b.ReportMetric(..., "heap-MB")`. Expected: under 50 MB held, down from about 400 MB on the base commit (run the same benchmark there).
- [ ] **Step 6: Commit**

```bash
git add internal/proxy
git commit -m "perf(proxy): release request bodies once the response starts"
```

---

### Task 6: SSE scrubber returns whole buffered lines

**Files:**
- Modify: `internal/proxy/response.go` (`sseScrubber`, `newSSEScrubber`), `internal/proxy/response_test.go` (create it if absent; the SSE tests live in `proxy_test.go` today)

**Interfaces:**
- Produces: no new names. The behaviour changes as follows:
  - `newSSEScrubber` uses `bufio.NewReaderSize(src, 8<<10)`.
  - `fill` keeps going while `s.br.Buffered() > 0` and the buffered bytes contain a `'\n'` (`bytes.IndexByte` on `s.br.Peek(s.br.Buffered())`). It never calls a read that could block.
  - A whole line that is not an error and has no assembled prefix goes to `pending` without a copy.
  - Assembly reuses `s.line[:0]`.
  - Rewritten lines and passthrough or discard state work exactly as today.

- [ ] **Step 1: Write the failing tests**

```go
func TestSSEScrubberBatchesWithoutBlocking(t *testing.T)
// src = io.Pipe: write "data: {a}\n\ndata: {\"error\":{\"message\":\"bifrost x\"}}\n\ndata: {b}\n" (no final
// newline yet); one s.Read(32 KB) returns all complete lines, error rewritten (no "bifrost"), without waiting;
// the trailing partial line arrives on a later Read only after the writer sends "\n"
func TestSSEScrubberOneReadPerEvent(t *testing.T) // sseStream(100) through a bytes.Reader: Reads with n>0 ≤ 101
func TestSSEScrubberOutputUnchanged(t *testing.T)
// for every existing SSE fixture in proxy_test.go plus Persian ZWNJ split across lines: output == the old
// scrubber's output (keep a copy as sseScrubberRef in the test file only until this test passes, then compare
// against literal expected strings and delete the copy)
```

- [ ] **Step 2: Run them and confirm they fail.** Run `go test -race ./internal/proxy -run SSEScrubber`. Expected: FAIL (`OneReadPerEvent` sees about 200 reads).
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run them and confirm they pass.** Run `go test -race ./internal/proxy`. Expected: `ok`, with every `TestProxy_SSE*` test green and unedited.
- [ ] **Step 5: Benchmark.** Run `go test -run '^$' -bench SSEScrubber -benchmem -count 6 ./internal/proxy`. Expected: reads per event about 0.5 or fewer (down from 2.0), about 0 allocs per event, and 8 KB per stream.
- [ ] **Step 6: Commit**

```bash
git add internal/proxy
git commit -m "perf(proxy): forward every buffered SSE line in one read"
```

---

### Task 7: Header scrub fast path

**Files:**
- Modify: `internal/proxy/response.go`, `internal/proxy/response_test.go`

**Interfaces:**
- Produces:

```go
// mayHoldEngineWord reports, without allocating, whether b may contain "bifrost" or "cliproxyapi" in any ASCII
// case, or "sk-bf-". A false result is exact. A true result may be a false positive; scrub then decides.
func mayHoldEngineWord(b []byte) bool
// scrub: if !mayHoldEngineWord(b) { return b }
// scrubString: if !mayHoldEngineWord([]byte(s)) { return s } (the compiler does not copy for this call)
```

- [ ] **Step 1: Write the failing tests**

```go
func TestMayHoldEngineWord(t *testing.T) // table: "BiFrOsT", "x-CLIPROXYAPI", "sk-bf-1" → true; "Mon, 01 Jan 2026", "", "bifros" → false
func TestScrubUnchangedWhenClean(t *testing.T) // testing.AllocsPerRun(scrubString("Mon, 01 Jan 2026 00:00:00 GMT")) == 0
func FuzzScrubFastPath(f *testing.F)     // any bytes: scrub(b) equals engineKeys/engineWords ReplaceAll applied directly
```

- [ ] **Step 2: Run them and confirm they fail.** Run `go test -race ./internal/proxy -run 'EngineWord|ScrubUnchanged'`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run them and confirm they pass.** Run `go test -race ./internal/proxy && go test ./internal/proxy -run '^$' -fuzz FuzzScrubFastPath -fuzztime 30s`. Expected: `ok`.
- [ ] **Step 5: Benchmark.** Run `go test -run '^$' -bench CleanResponseHeaders -benchmem -count 6 ./internal/proxy`. Expected: under 1.5 µs and under 15 allocs, down from 5.5 µs and 81 allocs.
- [ ] **Step 6: Commit**

```bash
git add internal/proxy
git commit -m "perf(proxy): skip the scrub regexes on header values without an engine word"
```

---

### Task 8: Leak fix, front-door `extra_fields` and `X-Cpa-*`

Where the leak comes from: the front door gives every response type an `extra_fields` with no `omitempty`. The chat response type is `B/core/schemas/chatcompletions.go:49`. For streams it marshals the whole chunk (`B/transports/bifrost-http/handlers/inference.go:2521`, `BifrostStreamChunk.MarshalJSON` at `B/core/schemas/bifrost.go:2047`). So SSE chunks carry `extra_fields` too: `routing_info.key`, `provider` and `provider_response_headers`. Step 1's e2e test confirms this on the real image before the code changes.

**Files:**
- Create: `test/e2e/leak_test.go` (`//go:build e2e`)
- Modify: `internal/proxy/response.go` (`isEngineHeader`, `modifyResponse` JSON branch, SSE line path), `internal/proxy/response_test.go`, `internal/proxy/proxy.go`

**Interfaces:**
- Produces:

```go
// stripExtraFields removes the top-level "extra_fields" member, with its comma, from a JSON object. It returns b
// unchanged, with no allocation, when there is none or b is not an object. dst is reused when it has the capacity.
func stripExtraFields(dst, b []byte) []byte
// isEngineHeader: also strings.HasPrefix(l, "x-cpa-")
// modifyResponse: for a 2xx response that is not an event stream, with Content-Type application/json and
// ContentLength ≤ MaxBodyBytes (−1 is read up to MaxBodyBytes+1), read the body, strip it, and reset Body,
// Content-Length and ContentLength. A larger body passes through untouched.
// shortcut: a non-streamed body over 100 MB keeps its extra_fields; stream-strip it if one ever appears.
// sseScrubber: a complete "data:" line whose payload contains `"extra_fields"` (bytes.Contains first) has it
// stripped into s.line's reused buffer. A long line in passthrough is left as it is.
```

- [ ] **Step 1: Write the failing tests**

```go
// test/e2e/leak_test.go
func TestNoEngineDetailInResponses(t *testing.T)
// once(t,"a",RouteOpenAIChat, Step{Headers: {"X-Cpa-Trace-Id": "t1"}}) then a non-streamed chat to alias "fast":
//   top-level JSON keys of the body have no "extra_fields"; no response header name has the prefix x-cpa-, x-bf-
//   or contains bifrost; the body has no "routing_info", "sk-bf-" or "remora-oauth"
// streamed chat (TestChatStreamPersian's entries): no SSE data payload has a top-level "extra_fields"; the
//   Persian content still concatenates byte for byte
// embeddings: no top-level "extra_fields"
// internal/proxy/response_test.go
func TestStripExtraFieldsTopLevelOnly(t *testing.T)
// table: first, middle and last member; the only member (`{"extra_fields":{}}` → `{}`); with whitespace;
// nested {"choices":[{"message":{"content":"\"extra_fields\":1"}}],"extra_fields":{...}} → only the top one goes;
// Persian ZWNJ content byte for byte; not an object → unchanged and AllocsPerRun == 0
func TestCleanResponseHeadersDropsCpa(t *testing.T) // X-Cpa-Trace-Id and x-cpa-anything are removed; X-Request-Id is kept
func TestProxy_NonStreamStripsExtraFields(t *testing.T) // httptest upstream → Content-Length matches the stripped body
func TestProxy_SSEStripsExtraFields(t *testing.T)       // each chunk loses extra_fields; an error event is still rewritten
```

- [ ] **Step 2: Run them and confirm they fail.** Run `go test -race ./internal/proxy -run 'ExtraFields|Cpa'`. Expected: FAIL. Then run `make image fakes-image && go test -tags e2e -count=1 ./test/e2e -run TestNoEngineDetailInResponses`. Expected: FAIL in the non-streamed subtest, with `extra_fields` present. If the streamed subtest passes on the base image, record that in "Corrections found during implementation" and keep the SSE strip anyway: it is cheap and covers the Responses stream.
- [ ] **Step 3: Implement.** `stripExtraFields` uses `scanObject`.
- [ ] **Step 4: Run them and confirm they pass.** Run `go test -race ./internal/proxy`, then `make image && go test -tags e2e -count=1 ./test/e2e -run TestNoEngineDetailInResponses`. Expected: `ok`.
- [ ] **Step 5: Benchmark.** Extend `BenchmarkSSEScrubber` with a variant that carries `extra_fields` in every chunk, which is the real front-door shape. Expected: under 1 µs and about 0 allocs per event.
- [ ] **Step 6: Commit**

```bash
git add internal/proxy test/e2e/leak_test.go
git commit -m "fix(proxy): strip the front door's extra_fields and X-Cpa-* headers from responses"
```

---

### Task 9: Gates and measurement

- [ ] **Step 1: Unit gate.** Run `make lint test`. Expected: golangci-lint is clean and `go test -race ./...` reports `ok`.
- [ ] **Step 2: e2e gate.** Run `make image fakes-image e2e`. Expected: `ok`, including every existing streaming and Persian test, unedited, and `TestNoEngineDetailInResponses`.
- [ ] **Step 3: Micro benchmarks.** Run `go test -run '^$' -bench . -benchmem -count 6 ./internal/proxy ./internal/store > $TMPDIR/remora-bench-after.txt && go run golang.org/x/perf/cmd/benchstat@latest $TMPDIR/remora-bench-before.txt $TMPDIR/remora-bench-after.txt`.
  Expected, with every row significant at `p<0.05`:
  - `RequestPath/1024KB`: allocated bytes down more than 10×, from about 15 MB; time down at least 3×.
  - `PerRequestParallel`: time down at least 10×.
  - `SSEScrubber`: reads per event down from 2.0 to 0.5 or fewer.
  - `CleanResponseHeaders`: allocs down at least 5×.
- [ ] **Step 4: Load benchmark.** Run `make bench` on the same machine plan 08 recorded its numbers on, and compare with them. Expected:
  - remora CPU per request, and per 100 streams, is down at every concurrency.
  - remora RSS at 500 streams is no higher.
  - Overhead p99 and time to first token are no worse.
  - Errors stay at zero.
  The front door's numbers do not move, since this plan does not touch it.
- [ ] **Step 5: Paste the results into the PR.** The benchstat table and the `make bench` deltas go in the PR description, not in the repo. Open the pull request from `perf-hot-path`:

```bash
git push -u origin perf-hot-path
gh pr create --title "perf: hot path in code" --body-file $TMPDIR/remora-pr2-body.md
```

## Corrections found during implementation

(Filled in during the work, as in plans 01–07.)
