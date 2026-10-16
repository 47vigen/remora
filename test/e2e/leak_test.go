//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/47vigen/remora/test/fakes/control"
)

// topLevelKeys returns the keys of the JSON object in b.
func topLevelKeys(t *testing.T, b []byte) map[string]json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	decodeJSON(t, b, &m)
	return m
}

// TestNoEngineDetailInResponses checks that the front door's extra_fields (internal provider names, key ids) and the
// OAuth engine's own headers never reach a client, on every response shape.
func TestNoEngineDetailInResponses(t *testing.T) {
	resetFakes(t)
	once(t, "a", control.RouteOpenAIChat, control.Step{Headers: map[string]string{"X-Cpa-Trace-Id": "t1"}})
	rep := chat(t, map[string]any{"model": "fast", "messages": []map[string]string{{"role": "user", "content": "hi"}}})
	wantStatus(t, rep, http.StatusOK)
	if _, ok := topLevelKeys(t, rep.Body)["extra_fields"]; ok {
		t.Errorf("non-streamed chat carries extra_fields: %s", rep.Body)
	}
	for name := range rep.Header {
		if strings.HasPrefix(strings.ToLower(name), "x-cpa-") {
			t.Errorf("response header %s reached the client", name)
		}
	}

	resetFakes(t)
	parts := []string{"می\u200c", "شود"}
	var sse []string
	for _, p := range parts {
		sse = append(sse, sseChunk(t, "c1", []map[string]any{{"index": 0, "delta": map[string]any{"content": p}}}, nil))
	}
	once(t, "a", control.RouteOpenAIChat, control.Step{SSE: sse})
	rep = chat(t, map[string]any{"model": "fast", "stream": true, "messages": []map[string]string{{"role": "user", "content": "hi"}}})
	var text strings.Builder
	for _, e := range sseData(t, rep.Body) {
		if e == "[DONE]" {
			continue
		}
		if _, ok := topLevelKeys(t, []byte(e))["extra_fields"]; ok {
			t.Errorf("stream chunk carries extra_fields: %s", e)
		}
	}
	for _, c := range streamChunks(t, rep) {
		for _, ch := range c.Choices {
			text.WriteString(ch.Delta.Content)
		}
	}
	if got := text.String(); got != strings.Join(parts, "") {
		t.Errorf("streamed text = %q, want %q", got, strings.Join(parts, ""))
	}

	rep = doV1(t, http.MethodPost, "/v1/embeddings", keyFull, map[string]any{"model": "emb", "input": "x"})
	wantStatus(t, rep, http.StatusOK)
	if _, ok := topLevelKeys(t, rep.Body)["extra_fields"]; ok {
		t.Errorf("embeddings carry extra_fields: %s", rep.Body)
	}
}
