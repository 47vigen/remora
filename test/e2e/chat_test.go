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

// chunk is the part of an OpenAI chat completion chunk the tests read.
type chunk struct {
	ID      string `json:"id"`
	Choices []struct {
		Index int `json:"index"`
		Delta struct {
			Role      string `json:"role"`
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens            int `json:"prompt_tokens"`
		CompletionTokens        int `json:"completion_tokens"`
		CompletionTokensDetails *struct {
			ReasoningTokens int `json:"reasoning_tokens"`
		} `json:"completion_tokens_details"`
	} `json:"usage"`
}

// streamChunks checks that rep is an SSE stream ending with [DONE] and returns its JSON chunks in order.
func streamChunks(t *testing.T, rep reply) []chunk {
	t.Helper()
	wantStatus(t, rep, http.StatusOK)
	if ct := rep.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content-type %q, want text/event-stream", ct)
	}
	events := sseData(t, rep.Body)
	if len(events) == 0 || events[len(events)-1] != "[DONE]" {
		t.Fatalf("stream does not end with data: [DONE]: %q", rep.Body)
	}
	out := make([]chunk, 0, len(events)-1)
	for _, e := range events[:len(events)-1] {
		var c chunk
		decodeJSON(t, []byte(e), &c)
		out = append(out, c)
	}
	return out
}

// sseChunk builds one SSE entry of a scripted stream: a chat completion chunk with id, choices and usage.
func sseChunk(t *testing.T, id string, choices []map[string]any, usage map[string]any) string {
	t.Helper()
	c := map[string]any{
		"id": id, "object": "chat.completion.chunk", "created": control.DefaultCreated, "model": "m1",
		"choices": choices,
	}
	if usage != nil {
		c["usage"] = usage
	}
	return mustJSON(t, c)
}

func TestChatStreamPersian(t *testing.T) {
	resetFakes(t)
	// The third piece starts with a ZWNJ, so a split inside the text must survive the relay.
	parts := []string{"می\u200c", "شود و نمی", "\u200cخواهم"}
	if strings.Join(parts, "") != persian {
		t.Fatal("parts do not concatenate to the fixture")
	}
	entries := []string{
		sseChunk(t, "chatcmpl-1", []map[string]any{{"index": 0, "delta": map[string]any{"role": "assistant", "content": parts[0]}, "finish_reason": nil}}, nil),
		sseChunk(t, "chatcmpl-1", []map[string]any{{"index": 0, "delta": map[string]any{"content": parts[1]}, "finish_reason": nil}}, nil),
		sseChunk(t, "chatcmpl-1", []map[string]any{{"index": 0, "delta": map[string]any{"content": parts[2]}, "finish_reason": "stop"}}, nil),
		sseChunk(t, "chatcmpl-1", []map[string]any{}, map[string]any{
			"prompt_tokens": 3, "completion_tokens": 5, "total_tokens": 8,
			"completion_tokens_details": map[string]any{"reasoning_tokens": 2},
		}),
	}
	once(t, "a", control.RouteOpenAIChat, control.Step{SSE: entries})

	rep := chat(t, map[string]any{
		"model": "fast", "stream": true, "stream_options": map[string]any{"include_usage": true},
		"messages": []map[string]any{{"role": "user", "content": persian}},
	})
	chunks := streamChunks(t, rep)

	var content bytes.Buffer
	for i, c := range chunks {
		if c.ID != "chatcmpl-1" {
			t.Errorf("chunk %d id %q, want chatcmpl-1", i, c.ID)
		}
		for _, ch := range c.Choices {
			content.WriteString(ch.Delta.Content)
		}
	}
	if !bytes.Equal(content.Bytes(), []byte(persian)) {
		t.Errorf("content bytes %x, want %x", content.Bytes(), []byte(persian))
	}
	last := chunks[len(chunks)-1]
	if last.Usage == nil || last.Usage.CompletionTokensDetails == nil || last.Usage.CompletionTokensDetails.ReasoningTokens != 2 {
		t.Errorf("last chunk usage %+v, want reasoning_tokens 2; body %s", last.Usage, rep.Body)
	}
	wantHeader(t, rep, "X-Remora-Alias", "fast")
	wantHeader(t, rep, "X-Remora-Served-By", "up-a/m1")
	wantHeader(t, rep, "X-Remora-Fallback", "false")
}

func TestChatNonStreamPersian(t *testing.T) {
	resetFakes(t)
	rep := chat(t, map[string]any{
		"model":    "fast",
		"messages": []map[string]any{{"role": "user", "content": persian}},
	})
	wantStatus(t, rep, http.StatusOK)
	var got struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	decodeJSON(t, rep.Body, &got)
	if len(got.Choices) == 0 || !bytes.Equal([]byte(got.Choices[0].Message.Content), []byte(persian)) {
		t.Errorf("content %q, want %q", got.Choices, persian)
	}
	// The fake echoes the model it was asked for: the member's model, not the alias.
	if got.Model != "m1" {
		t.Errorf("model %q, want the fake's m1", got.Model)
	}
	wantHeader(t, rep, "X-Remora-Alias", "fast")
	wantHeader(t, rep, "X-Remora-Served-By", "up-a/m1")
	wantHeader(t, rep, "X-Remora-Fallback", "false")
}

func TestJSONSchemaPassthrough(t *testing.T) {
	resetFakes(t)
	format := json.RawMessage(`{"type":"json_schema","json_schema":{"name":"r","strict":true,"schema":{` +
		`"type":"object","properties":{"answer":{"type":"string"},"score":{"type":"number"}},` +
		`"required":["answer","score"],"additionalProperties":false}}}`)
	rep := chat(t, map[string]any{
		"model":           "fast",
		"messages":        []map[string]any{{"role": "user", "content": "answer"}},
		"response_format": format,
	})
	wantStatus(t, rep, http.StatusOK)

	reqs := received(t, "a", control.RouteOpenAIChat)
	if len(reqs) != 1 {
		t.Fatalf("up-a got %d chat requests, want 1", len(reqs))
	}
	var sent struct {
		ResponseFormat json.RawMessage `json:"response_format"`
	}
	decodeJSON(t, reqs[0].Body, &sent)
	if !jsonEqual(t, sent.ResponseFormat, format) {
		t.Errorf("upstream response_format %s, want %s", sent.ResponseFormat, format)
	}
}

func TestToolCallsInStream(t *testing.T) {
	resetFakes(t)
	tc := func(fields map[string]any) []map[string]any {
		return []map[string]any{{"index": 0, "delta": map[string]any{"tool_calls": []map[string]any{fields}}, "finish_reason": nil}}
	}
	args := []string{`{"q":`, `"می` + "\u200c" + `شود"`, `}`}
	entries := []string{
		sseChunk(t, "chatcmpl-2", []map[string]any{{"index": 0, "delta": map[string]any{"role": "assistant", "tool_calls": []map[string]any{{
			"index": 0, "id": "call_1", "type": "function", "function": map[string]any{"name": "lookup", "arguments": ""},
		}}}, "finish_reason": nil}}, nil),
		sseChunk(t, "chatcmpl-2", tc(map[string]any{"index": 0, "function": map[string]any{"arguments": args[0]}}), nil),
		sseChunk(t, "chatcmpl-2", tc(map[string]any{"index": 0, "function": map[string]any{"arguments": args[1]}}), nil),
		sseChunk(t, "chatcmpl-2", tc(map[string]any{"index": 0, "function": map[string]any{"arguments": args[2]}}), nil),
		sseChunk(t, "chatcmpl-2", []map[string]any{{"index": 0, "delta": map[string]any{}, "finish_reason": "tool_calls"}}, nil),
	}
	once(t, "a", control.RouteOpenAIChat, control.Step{SSE: entries})

	rep := chat(t, map[string]any{
		"model": "fast", "stream": true,
		"messages": []map[string]any{{"role": "user", "content": "look it up"}},
		"tools": []map[string]any{{"type": "function", "function": map[string]any{
			"name": "lookup", "parameters": map[string]any{"type": "object", "properties": map[string]any{"q": map[string]any{"type": "string"}}},
		}}},
	})
	chunks := streamChunks(t, rep)

	type call struct {
		id, name string
		args     bytes.Buffer
	}
	calls := map[int]*call{}
	var finish string
	for _, c := range chunks {
		for _, ch := range c.Choices {
			if ch.FinishReason != nil && *ch.FinishReason != "" {
				finish = *ch.FinishReason
			}
			for _, d := range ch.Delta.ToolCalls {
				cl := calls[d.Index]
				if cl == nil {
					cl = &call{}
					calls[d.Index] = cl
				}
				if d.ID != "" {
					cl.id = d.ID
				}
				cl.name += d.Function.Name
				cl.args.WriteString(d.Function.Arguments)
			}
		}
	}
	if len(calls) != 1 || calls[0] == nil {
		t.Fatalf("assembled %d tool calls, want 1; body %s", len(calls), rep.Body)
	}
	got := calls[0]
	if got.id != "call_1" || got.name != "lookup" {
		t.Errorf("tool call id %q name %q, want call_1 lookup", got.id, got.name)
	}
	want := `{"q":"می` + "\u200c" + `شود"}`
	if !bytes.Equal(got.args.Bytes(), []byte(want)) {
		t.Errorf("arguments %q, want %q", got.args.Bytes(), want)
	}
	if finish != "tool_calls" {
		t.Errorf("finish_reason %q, want tool_calls", finish)
	}
}

func TestUnknownParamReachesUpstream(t *testing.T) {
	resetFakes(t)
	extra := json.RawMessage(`{"a":1}`)
	rep := chat(t, map[string]any{
		"model":            "fast",
		"messages":         []map[string]any{{"role": "user", "content": "hi"}},
		"remora_e2e_extra": extra,
	})
	wantStatus(t, rep, http.StatusOK)
	reqs := received(t, "a", control.RouteOpenAIChat)
	if len(reqs) != 1 {
		t.Fatalf("up-a got %d chat requests, want 1", len(reqs))
	}
	var sent map[string]json.RawMessage
	decodeJSON(t, reqs[0].Body, &sent)
	got, ok := sent["remora_e2e_extra"]
	if !ok || !jsonEqual(t, got, extra) {
		t.Errorf("upstream body %s, want remora_e2e_extra %s", reqs[0].Body, extra)
	}
}
