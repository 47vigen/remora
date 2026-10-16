//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/47vigen/remora/test/fakes/control"
)

// TestReasoningNotTripled: reasoning reaches the client as reasoning and reasoning_content, never a third time as a
// synthesised reasoning_details entry.
func TestReasoningNotTripled(t *testing.T) {
	resetFakes(t)
	once(t, "a", control.RouteOpenAIChat, control.Step{Body: mustJSON(t, map[string]any{
		"id": "c1", "object": "chat.completion", "created": control.DefaultCreated, "model": "m1",
		"choices": []map[string]any{{"index": 0, "finish_reason": "stop", "message": map[string]any{
			"role": "assistant", "content": persian, "reasoning_content": "r",
		}}},
		"usage": map[string]any{"prompt_tokens": 3, "completion_tokens": 5, "total_tokens": 8},
	})})
	rep := chat(t, chatBody("fast", false))
	wantStatus(t, rep, http.StatusOK)
	var got struct {
		Choices []struct {
			Message map[string]json.RawMessage `json:"message"`
		} `json:"choices"`
	}
	decodeJSON(t, rep.Body, &got)
	if len(got.Choices) != 1 {
		t.Fatalf("choices: %s", rep.Body)
	}
	m := got.Choices[0].Message
	if string(m["reasoning"]) != `"r"` || string(m["reasoning_content"]) != `"r"` {
		t.Errorf("reasoning %s, reasoning_content %s; want both \"r\"", m["reasoning"], m["reasoning_content"])
	}
	if _, ok := m["reasoning_details"]; ok {
		t.Errorf("message carries reasoning_details: %s", rep.Body)
	}

	resetFakes(t)
	once(t, "a", control.RouteOpenAIChat, control.Step{SSE: []string{
		sseChunk(t, "c1", []map[string]any{{"index": 0, "delta": map[string]any{"reasoning_content": "r"}}}, nil),
		sseChunk(t, "c1", []map[string]any{{"index": 0, "delta": map[string]any{"content": persian}}}, nil),
	}})
	rep = chat(t, chatBody("fast", true))
	streamChunks(t, rep)
	var content bytes.Buffer
	sawReasoning := false
	for _, raw := range sseData(t, rep.Body) {
		if raw == "[DONE]" {
			continue
		}
		if strings.Contains(raw, "reasoning_details") {
			t.Errorf("chunk carries reasoning_details: %s", raw)
		}
		if strings.Contains(raw, `"reasoning_content":"r"`) {
			sawReasoning = true
		}
		var ev chunk
		decodeJSON(t, []byte(raw), &ev)
		for _, c := range ev.Choices {
			content.WriteString(c.Delta.Content)
		}
	}
	if !sawReasoning {
		t.Error("no chunk carried reasoning_content")
	}
	if !bytes.Equal(content.Bytes(), []byte(persian)) {
		t.Errorf("content %q, want %q", content.String(), persian)
	}
}
