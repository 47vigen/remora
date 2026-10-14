//go:build e2e

package e2e

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/47vigen/remora/test/fakes/control"
)

// The reference deployment answered 504 to a huge non-streamed prompt because of a 15 s queue deadline, and cut
// nothing else shorter (docs/research/2026-10-14-reference-deployment.md, MUST NOT 7). The engines here are
// configured far longer: 600 s per request and 300 s between stream chunks (internal/engine/bifrost/providers.go),
// too long to wait out in CI. So the deadline tests below outlast the reference's 15 s, the shortest deadline the
// requirement names, with a few seconds to spare.
const shortDeadline = 15 * time.Second

// Names the tests in this file create through the management API; nothing in testdata/remora.yaml or another test
// uses them. Each provider is the fake upstream of the same name.
const (
	loadProvider = "load"
	loadAlias    = "e2e-load"
	failAlias    = "e2e-failing"
)

var failProviders = []string{"fail-a", "fail-b", "fail-c"}

// chatCompletion is a non-streamed chat answer for a scripted step.
func chatCompletion(t *testing.T, content string) string {
	t.Helper()
	return mustJSON(t, map[string]any{
		"id": "chatcmpl-load", "object": "chat.completion", "created": control.DefaultCreated, "model": "m1",
		"choices": []map[string]any{{"index": 0, "message": map[string]any{"role": "assistant", "content": content}, "finish_reason": "stop"}},
		"usage":   map[string]any{"prompt_tokens": 3, "completion_tokens": 5, "total_tokens": 8},
	})
}

// bigPrompt returns a user message of about n bytes, ZWNJ included.
func bigPrompt(n int) string {
	const word = "می\u200cشود lorem ipsum "
	return strings.Repeat(word, n/len(word)+1)
}

// setupChain creates one openai-compatible provider per name on the fakes and a chat alias over their m1 models, in
// order, then waits until the alias serves the full key. The returned func deletes them again; defer it (the
// requests need the test's context, which ends before cleanups run). A leftover of an earlier run is removed first.
func setupChain(t *testing.T, a *admin, alias string, providers ...string) func() {
	t.Helper()
	cleanup := func() {
		a.deleteIfAny("/api/aliases/" + url.PathEscape(alias))
		for _, p := range providers {
			a.deleteIfAny("/api/providers/" + url.PathEscape(p))
		}
	}
	cleanup()
	members := make([]string, 0, len(providers))
	for _, p := range providers {
		a.must(http.MethodPost, "/api/providers", mustJSON(t, map[string]any{
			"name": p, "kind": "openai-compatible", "base_url": "http://fakes:9090/u/" + p,
			"allow_private_network": true, "keys": []map[string]string{{"name": "main", "value": "fake-key"}},
		}), http.StatusCreated)
		members = append(members, p+"/m1")
	}
	a.must(http.MethodPost, "/api/aliases", mustJSON(t, map[string]any{
		"name": alias, "kind": "chat", "members": members,
	}), http.StatusCreated)
	// The engines pick up new providers shortly after they are stored; the fakes' default answer is a 200.
	eventually(t, 30*time.Second, "chat through "+alias, func() bool {
		return chat(t, chatMessage(alias, "ready?")).Status == http.StatusOK
	})
	return cleanup
}

// rawResult is one /v1 answer read outside the test goroutine.
type rawResult struct {
	rep reply
	err error
}

// postChat sends a chat body with the full key and reads the whole answer. It never touches t, so it may run in
// any goroutine.
func postChat(ctx context.Context, base string, body []byte) rawResult {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return rawResult{err: err}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+keyFull)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return rawResult{err: err}
	}
	defer func() { _ = resp.Body.Close() }()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		return rawResult{err: err}
	}
	return rawResult{rep: reply{Status: resp.StatusCode, Header: resp.Header, Body: out}}
}

// TestConcurrentHeavyRequests proves there is no global single-request admission: several heavy requests (a large
// prompt each, a slow upstream) sent at once all succeed, none is shed with a 503, all are in flight upstream
// together, and the whole batch takes about as long as one of them.
func TestConcurrentHeavyRequests(t *testing.T) {
	const (
		n     = 4
		delay = 6 * time.Second
		size  = 1 << 20 // 1 MiB of prompt each
	)
	resetFakes(t)
	defer resetFakes(t)
	a := loginClient(t)
	defer setupChain(t, a, loadAlias, loadProvider)()
	resetFakes(t) // drop the readiness probes from the recordings
	script(t, loadProvider, control.RouteOpenAIChat, control.Script{
		Steps: []control.Step{{DelayMS: int(delay / time.Millisecond), Body: chatCompletion(t, persian)}}, Repeat: true,
	})

	body := []byte(mustJSON(t, chatMessage(loadAlias, bigPrompt(size))))
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	base := remoraURL(t, "")
	results := make([]rawResult, n)
	var wg sync.WaitGroup
	start := time.Now()
	for i := range n {
		wg.Go(func() { results[i] = postChat(ctx, base, body) })
	}
	wg.Wait()
	elapsed := time.Since(start)

	for i, r := range results {
		if r.err != nil {
			t.Errorf("request %d: %v", i, r.err)
			continue
		}
		checkLeaks(t, fmt.Sprintf("request %d", i), r.rep)
		if r.rep.Status == http.StatusServiceUnavailable {
			t.Errorf("request %d was shed with 503; body %s", i, r.rep.Body)
		} else if r.rep.Status != http.StatusOK {
			t.Errorf("request %d: status %d, want 200; body %s", i, r.rep.Status, r.rep.Body)
		}
	}
	// One at a time would take n*delay; two at a time already 2*delay.
	if elapsed >= 2*delay {
		t.Errorf("%d requests of %s each took %s together, want under %s: they did not run concurrently", n, delay, elapsed, 2*delay)
	}
	reqs := received(t, loadProvider, control.RouteOpenAIChat)
	if len(reqs) != n {
		t.Fatalf("the upstream got %d chat requests, want %d", len(reqs), n)
	}
	first, last := reqs[0].At, reqs[0].At
	for _, r := range reqs {
		if len(r.Body) < size {
			t.Errorf("an upstream request carried %d bytes, want the whole prompt (at least %d)", len(r.Body), size)
		}
		if r.At.Before(first) {
			first = r.At
		}
		if r.At.After(last) {
			last = r.At
		}
	}
	// Every request reached the upstream before the first one could have been answered.
	if spread := last.Sub(first); spread >= delay {
		t.Errorf("the upstream received the requests %s apart, want all within %s (in flight together)", spread, delay)
	}
}

// TestLargePromptNoQueueDeadline proves a very large non-streamed prompt waits for a slow upstream past the short
// queue deadline and answers 200, never 504.
func TestLargePromptNoQueueDeadline(t *testing.T) {
	const (
		delay = shortDeadline + 5*time.Second
		size  = 4 << 20 // 4 MiB, about a million tokens
	)
	resetFakes(t)
	defer resetFakes(t)
	a := loginClient(t)
	defer setupChain(t, a, loadAlias, loadProvider)()
	resetFakes(t)
	once(t, loadProvider, control.RouteOpenAIChat, control.Step{DelayMS: int(delay / time.Millisecond), Body: chatCompletion(t, persian)})

	prompt := bigPrompt(size)
	start := time.Now()
	rep := chat(t, chatMessage(loadAlias, prompt))
	elapsed := time.Since(start)
	if rep.Status == http.StatusGatewayTimeout {
		t.Fatalf("504 after %s; body %s", elapsed, rep.Body)
	}
	wantStatus(t, rep, http.StatusOK)
	if elapsed < delay {
		t.Errorf("answered after %s, before the upstream could (%s)", elapsed, delay)
	}
	var got struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	decodeJSON(t, rep.Body, &got)
	if len(got.Choices) == 0 || !bytes.Equal([]byte(got.Choices[0].Message.Content), []byte(persian)) {
		t.Errorf("answer %s, want the upstream's content %q", rep.Body, persian)
	}
	reqs := received(t, loadProvider, control.RouteOpenAIChat)
	if len(reqs) != 1 {
		t.Fatalf("the upstream got %d chat requests, want 1", len(reqs))
	}
	var sent struct {
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	decodeJSON(t, reqs[0].Body, &sent)
	if len(sent.Messages) != 1 || sent.Messages[0].Content != prompt {
		t.Errorf("the upstream did not get the whole prompt (%d bytes sent)", len(prompt))
	}
}

// TestLongStreamNoDeadline proves a stream that lasts longer than the short deadline is relayed live, chunk by
// chunk, and completes with [DONE].
func TestLongStreamNoDeadline(t *testing.T) {
	const (
		pieces   = 10
		interval = 2 * time.Second // pieces intervals in all, [DONE] included: 20 s
	)
	resetFakes(t)
	defer resetFakes(t)
	a := loginClient(t)
	defer setupChain(t, a, loadAlias, loadProvider)()
	resetFakes(t)

	var want strings.Builder
	entries := make([]string, 0, pieces+1)
	for i := range pieces {
		piece := fmt.Sprintf("تکه\u200cی %d ", i)
		want.WriteString(piece)
		delta := map[string]any{"content": piece}
		if i == 0 {
			delta["role"] = "assistant"
		}
		entries = append(entries, sseChunk(t, "chatcmpl-long", []map[string]any{{"index": 0, "delta": delta, "finish_reason": nil}}, nil))
	}
	entries = append(entries, sseChunk(t, "chatcmpl-long", []map[string]any{{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}}, nil))
	once(t, loadProvider, control.RouteOpenAIChat, control.Step{SSE: entries, IntervalMS: int(interval / time.Millisecond)})
	total := time.Duration(len(entries)) * interval
	if total <= shortDeadline {
		t.Fatalf("the scripted stream lasts %s, not longer than %s", total, shortDeadline)
	}

	body := chatMessage(loadAlias, persian)
	body["stream"] = true
	ctx, cancel := context.WithTimeout(t.Context(), total+time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, remoraURL(t, "/v1/chat/completions"), strings.NewReader(mustJSON(t, body)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+keyFull)
	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/chat/completions: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	// Read line by line to time the first event: a relay that buffered the stream would hold it to the end.
	var raw bytes.Buffer
	var firstAt time.Duration
	r := bufio.NewReader(resp.Body)
	for {
		line, err := r.ReadString('\n')
		raw.WriteString(line)
		if firstAt == 0 && strings.HasPrefix(line, "data:") {
			firstAt = time.Since(start)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read stream after %s: %v; got so far %q", time.Since(start), err, raw.Bytes())
		}
	}
	elapsed := time.Since(start)
	rep := reply{Status: resp.StatusCode, Header: resp.Header, Body: raw.Bytes()}
	checkLeaks(t, "POST /v1/chat/completions", rep)
	chunks := streamChunks(t, rep) // 200, text/event-stream, ends with [DONE]

	var content bytes.Buffer
	for _, c := range chunks {
		for _, ch := range c.Choices {
			content.WriteString(ch.Delta.Content)
		}
	}
	if !bytes.Equal(content.Bytes(), []byte(want.String())) {
		t.Errorf("content %q, want %q", content.Bytes(), want.String())
	}
	if elapsed <= shortDeadline {
		t.Errorf("the stream ended after %s, want it to outlast %s", elapsed, shortDeadline)
	}
	if firstAt >= total/2 {
		t.Errorf("the first event arrived after %s of %s: the stream was not relayed live", firstAt, elapsed)
	}
}

// TestFailingChainFailsFast proves a chain whose every member fails answers an error at once: each member is tried
// once, without retries or waits, and the answer comes in bounded time.
func TestFailingChainFailsFast(t *testing.T) {
	const bound = 5 * time.Second
	resetFakes(t)
	defer resetFakes(t)
	a := loginClient(t)
	defer setupChain(t, a, failAlias, failProviders...)()
	resetFakes(t)
	failures := []control.Step{
		{Status: http.StatusTooManyRequests, Body: `{"error":{"type":"rate_limit_exceeded","message":"slow down"}}`},
		{Status: http.StatusPaymentRequired, Body: `{"error":{"message":"out of credit"}}`},
		{Status: http.StatusTooManyRequests, Body: `{"error":{"code":"insufficient_quota","message":"quota"}}`},
	}
	for i, p := range failProviders {
		script(t, p, control.RouteOpenAIChat, control.Script{Steps: []control.Step{failures[i]}, Repeat: true})
	}

	start := time.Now()
	rep := chat(t, chatMessage(failAlias, persian))
	elapsed := time.Since(start)
	if rep.Status < 400 {
		t.Fatalf("status %d, want an error; body %s", rep.Status, rep.Body)
	}
	if msg := errorMessage(t, rep); msg == "" {
		t.Errorf("no error.message; body %s", rep.Body)
	}
	if elapsed > bound {
		t.Errorf("the failing chain answered after %s, want within %s", elapsed, bound)
	}
	for _, p := range failProviders {
		if n := len(received(t, p, control.RouteOpenAIChat)); n != 1 {
			t.Errorf("%s got %d chat requests, want 1 (each member once, no retries)", p, n)
		}
	}
}

// TestTranscriptionLogged proves an audio request shows in the request logs of the management API, under the key
// that sent it and the provider that served it.
func TestTranscriptionLogged(t *testing.T) {
	sonioxSetup(t, twoSpeakers(t))
	a := loginClient(t)
	var keys struct {
		Items []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"items"`
	}
	decodeJSON(t, a.must(http.MethodGet, "/api/keys?limit=200", "", http.StatusOK), &keys)
	var speechID string
	for _, k := range keys.Items {
		if k.Name == "speech" {
			speechID = k.ID
		}
	}
	if speechID == "" {
		t.Fatalf("no key named speech in %+v", keys.Items)
	}

	from := time.Now().UTC().Add(-time.Second)
	rep := transcribe(t, 60*time.Second, field{"model", sonioxModel}, field{"response_format", "json"})
	wantStatus(t, rep, http.StatusOK)

	q := url.Values{"key": {speechID}, "from": {from.Format(time.RFC3339)}}.Encode()
	var entries []mgmtLog
	eventually(t, 15*time.Second, "a log entry for the transcription", func() bool {
		entries, _ = a.logs(q)
		return len(entries) >= 1
	})
	e := entries[0]
	if e.Key != speechID || e.Provider != "soniox" || e.Model != "stt-async-v5" || e.Status != "success" {
		t.Errorf("log entry %+v: want key %s, provider soniox, model stt-async-v5, status success", e, speechID)
	}
}
