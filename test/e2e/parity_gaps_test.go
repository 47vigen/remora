//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/47vigen/remora/test/fakes/control"
)

// The parity gap tests: each proves one row of docs/parity.md that no earlier plan's test covers.

// strictSchema is a strict json_schema response_format, as the memory service and the catalog miner send it.
var strictSchema = json.RawMessage(`{"type":"json_schema","json_schema":{"name":"r","strict":true,"schema":{` +
	`"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"],"additionalProperties":false}}}`)

// chatAnswer is the part of a non-streamed chat completion these tests read.
type chatAnswer struct {
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Role      string          `json:"role"`
			Content   *string         `json:"content"`
			ToolCalls json.RawMessage `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
}

// answerContent decodes a non-streamed chat completion and returns the first choice's content.
func answerContent(t *testing.T, rep reply) string {
	t.Helper()
	var a chatAnswer
	decodeJSON(t, rep.Body, &a)
	if len(a.Choices) == 0 || a.Choices[0].Message.Content == nil {
		t.Fatalf("no content in the answer: %s", rep.Body)
	}
	return *a.Choices[0].Message.Content
}

// streamContent concatenates the content deltas of a streamed chat completion.
func streamContent(t *testing.T, rep reply) string {
	t.Helper()
	var b strings.Builder
	for _, c := range streamChunks(t, rep) {
		for _, ch := range c.Choices {
			b.WriteString(ch.Delta.Content)
		}
	}
	return b.String()
}

// sentFields decodes one recorded upstream body into its top-level fields.
func sentFields(t *testing.T, r control.RecordedRequest) map[string]json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	decodeJSON(t, r.Body, &m)
	return m
}

// onlyRequest returns the single request an upstream got on a route, failing the test on any other count.
func onlyRequest(t *testing.T, upstream, route string) control.RecordedRequest {
	t.Helper()
	reqs := received(t, upstream, route)
	if len(reqs) != 1 {
		t.Fatalf("%s got %d requests on %s, want 1", upstream, len(reqs), route)
	}
	return reqs[0]
}

// gapFixture creates providers and aliases through the management API and returns the func that deletes them
// again, aliases first. It deletes leftovers of an earlier run before creating anything. Call the returned func with
// defer, not t.Cleanup: it needs the test's context.
func gapFixture(t *testing.T, providers, aliases []map[string]any) func() {
	t.Helper()
	a := loginClient(t)
	remove := func() {
		for _, al := range aliases {
			a.deleteIfAny("/api/aliases/" + url.PathEscape(al["name"].(string)))
		}
		for _, p := range providers {
			a.deleteIfAny("/api/providers/" + url.PathEscape(p["name"].(string)))
		}
	}
	remove()
	for _, p := range providers {
		a.must(http.MethodPost, "/api/providers", mustJSON(t, p), http.StatusCreated)
	}
	for _, al := range aliases {
		a.must(http.MethodPost, "/api/aliases", mustJSON(t, al), http.StatusCreated)
	}
	return remove
}

// awaitChat retries a chat completion until it is served: the engines pick up a new provider shortly after it is
// stored. It returns the first 200.
func awaitChat(t *testing.T, body any) reply {
	t.Helper()
	var rep reply
	deadline := time.Now().Add(30 * time.Second)
	for {
		rep = chat(t, body)
		if rep.Status == http.StatusOK {
			return rep
		}
		if time.Now().After(deadline) {
			t.Fatalf("not served after 30s: status %d, body %s", rep.Status, rep.Body)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// M2.1: strict json_schema honoured on every chain member, unfenced.
func TestJSONSchemaAcrossFallback(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run("stream="+strconv.FormatBool(stream), func(t *testing.T) {
			resetFakes(t)
			once(t, "a", control.RouteOpenAIChat, control.Step{Status: http.StatusTooManyRequests, Body: `{"error":{"type":"rate_limit_exceeded"}}`})

			body := map[string]any{
				"model":           "fast",
				"messages":        []map[string]any{{"role": "user", "content": persian}},
				"response_format": strictSchema,
			}
			if stream {
				body["stream"] = true
			}
			rep := chat(t, body)
			wantStatus(t, rep, http.StatusOK)
			wantHeader(t, rep, "X-Remora-Served-By", "up-b/m1")
			wantHeader(t, rep, "X-Remora-Fallback", "true")

			// Every member of the chain got the schema unchanged: the one that failed and the one that served.
			for _, up := range []string{"a", "b"} {
				sent := sentFields(t, onlyRequest(t, up, control.RouteOpenAIChat))
				if !jsonEqual(t, sent["response_format"], strictSchema) {
					t.Errorf("up-%s response_format %s, want %s", up, sent["response_format"], strictSchema)
				}
			}

			// The answer is the member's JSON as it sent it: no Markdown fence, nothing around it.
			var content string
			if stream {
				content = streamContent(t, rep)
			} else {
				content = answerContent(t, rep)
			}
			if content != control.DefaultChatJSONContent {
				t.Errorf("content %q, want the member's JSON %q unfenced", content, control.DefaultChatJSONContent)
			}
			var v struct {
				Answer string `json:"answer"`
			}
			if err := json.Unmarshal([]byte(content), &v); err != nil || v.Answer != persian {
				t.Errorf("content %q does not parse as the schema's object: %v", content, err)
			}
		})
	}
}

// M2.2: response_format text.
func TestResponseFormatText(t *testing.T) {
	resetFakes(t)
	format := json.RawMessage(`{"type":"text"}`)
	rep := chat(t, map[string]any{
		"model":           "fast",
		"stream":          false,
		"messages":        []map[string]any{{"role": "user", "content": persian}},
		"response_format": format,
	})
	wantStatus(t, rep, http.StatusOK)
	sent := sentFields(t, onlyRequest(t, "a", control.RouteOpenAIChat))
	if !jsonEqual(t, sent["response_format"], format) {
		t.Errorf("upstream response_format %s, want %s", sent["response_format"], format)
	}
	if got := answerContent(t, rep); !bytes.Equal([]byte(got), []byte(control.DefaultChatContent)) {
		t.Errorf("content %q, want the plain text %q", got, control.DefaultChatContent)
	}
}

// M3: tools with tool_choice auto and a multi-turn tool history.
func TestToolsMultiTurn(t *testing.T) {
	resetFakes(t)
	tools := json.RawMessage(`[{"type":"function","function":{"name":"lookup","description":"Look a word up.",` +
		`"parameters":{"type":"object","properties":{"q":{"type":"string"}},"required":["q"]}}}]`)
	args := `{"q":"می` + "\u200c" + `شود"}`
	toolCalls := json.RawMessage(`[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":` +
		strconv.Quote(args) + `}}]`)
	once(t, "a", control.RouteOpenAIChat, control.Step{Body: mustJSON(t, map[string]any{
		"id": "chatcmpl-tool", "object": "chat.completion", "created": control.DefaultCreated, "model": "m1",
		"choices": []map[string]any{{"index": 0, "finish_reason": "tool_calls", "message": map[string]any{
			"role": "assistant", "content": nil, "tool_calls": toolCalls,
		}}},
		"usage": map[string]any{"prompt_tokens": 3, "completion_tokens": 5, "total_tokens": 8},
	})})

	// Turn 1: the model asks for the tool.
	user := map[string]any{"role": "user", "content": "look up " + persian}
	rep := chat(t, map[string]any{
		"model": "fast", "messages": []map[string]any{user}, "tools": tools, "tool_choice": "auto",
	})
	wantStatus(t, rep, http.StatusOK)
	var first chatAnswer
	decodeJSON(t, rep.Body, &first)
	if len(first.Choices) != 1 || first.Choices[0].FinishReason != "tool_calls" || !sameToolCalls(t, first.Choices[0].Message.ToolCalls, toolCalls) {
		t.Fatalf("turn 1 answer %s, want the tool call %s with finish_reason tool_calls", rep.Body, toolCalls)
	}

	// Turn 2: the client replays the history with the tool's result; the default answer closes the turn.
	history := []map[string]any{
		user,
		{"role": "assistant", "tool_calls": first.Choices[0].Message.ToolCalls},
		{"role": "tool", "tool_call_id": "call_1", "content": persian},
	}
	rep = chat(t, map[string]any{"model": "fast", "messages": history, "tools": tools, "tool_choice": "auto"})
	wantStatus(t, rep, http.StatusOK)
	if got := answerContent(t, rep); got != control.DefaultChatContent {
		t.Errorf("turn 2 content %q, want %q", got, control.DefaultChatContent)
	}

	reqs := received(t, "a", control.RouteOpenAIChat)
	if len(reqs) != 2 {
		t.Fatalf("up-a got %d chat requests, want 2", len(reqs))
	}
	for i, r := range reqs {
		sent := sentFields(t, r)
		if string(sent["tool_choice"]) != `"auto"` {
			t.Errorf("turn %d: upstream tool_choice %s, want \"auto\"", i+1, sent["tool_choice"])
		}
		if !jsonEqual(t, sent["tools"], tools) {
			t.Errorf("turn %d: upstream tools %s, want %s", i+1, sent["tools"], tools)
		}
	}
	// The whole history reaches the member: the call, its id and arguments, and the tool's result, byte for byte.
	var got struct {
		Messages []struct {
			Role       string          `json:"role"`
			Content    *string         `json:"content"`
			ToolCalls  json.RawMessage `json:"tool_calls"`
			ToolCallID string          `json:"tool_call_id"`
		} `json:"messages"`
	}
	decodeJSON(t, reqs[1].Body, &got)
	if len(got.Messages) != 3 {
		t.Fatalf("turn 2: upstream got %d messages, want 3: %s", len(got.Messages), reqs[1].Body)
	}
	if m := got.Messages[0]; m.Role != "user" || m.Content == nil || *m.Content != user["content"] {
		t.Errorf("turn 2 message 1 %+v, want the user's question", m)
	}
	if m := got.Messages[1]; m.Role != "assistant" || !sameToolCalls(t, m.ToolCalls, toolCalls) {
		t.Errorf("turn 2 message 2: role %q tool_calls %s, want assistant %s", m.Role, m.ToolCalls, toolCalls)
	}
	if m := got.Messages[2]; m.Role != "tool" || m.ToolCallID != "call_1" || m.Content == nil || !bytes.Equal([]byte(*m.Content), []byte(persian)) {
		t.Errorf("turn 2 message 3 %+v, want the tool result for call_1", m)
	}
}

// sameToolCalls compares tool calls by id, type, name and arguments. The front door numbers non-streamed tool
// calls with an "index" field, as OpenAI does in streams; it carries nothing a client replays, so it is ignored.
func sameToolCalls(t *testing.T, got, want []byte) bool {
	t.Helper()
	var calls []map[string]any
	decodeJSON(t, got, &calls)
	for _, c := range calls {
		delete(c, "index")
	}
	return jsonEqual(t, []byte(mustJSON(t, calls)), want)
}

// M7.4: the OpenRouter provider kind.
func TestProviderOpenRouterKind(t *testing.T) {
	resetFakes(t)
	const (
		alias = "gap-openrouter"
		key   = "gap-openrouter-key"
	)
	defer gapFixture(t,
		[]map[string]any{{
			"name": "openrouter", "kind": "builtin", "type": "openrouter", "base_url": "http://fakes:9090/u/openrouter",
			"allow_private_network": true, "keys": []map[string]string{{"name": "main", "value": key}},
		}},
		[]map[string]any{{"name": alias, "kind": "chat", "members": []string{"openrouter/m1"}}},
	)()

	rep := awaitChat(t, chatMessage(alias, persian))
	wantHeader(t, rep, "X-Remora-Alias", alias)
	wantHeader(t, rep, "X-Remora-Served-By", "openrouter/m1")
	if got := answerContent(t, rep); got != control.DefaultChatContent {
		t.Errorf("content %q, want %q", got, control.DefaultChatContent)
	}
	reqs := received(t, "openrouter", control.RouteOpenAIChat)
	if len(reqs) == 0 {
		t.Fatal("the OpenRouter upstream got no chat request")
	}
	r := reqs[len(reqs)-1]
	if r.Path != "/u/openrouter/v1/chat/completions" {
		t.Errorf("upstream path %q, want OpenRouter's /v1/chat/completions below the base URL", r.Path)
	}
	if got := r.Header.Get("Authorization"); got != "Bearer "+key {
		t.Errorf("upstream Authorization does not carry the provider's key")
	}
	if sent := sentFields(t, r); string(sent["model"]) != `"m1"` {
		t.Errorf("upstream model %s, want \"m1\"", sent["model"])
	}
}

// M7.5: OpenAI-compatible providers on any base URL, including the API-key providers coding agents use, which
// publish their base URL with the /v1 suffix the OpenAI SDKs expect.
func TestProviderOpenAICompatible(t *testing.T) {
	cases := []struct{ name, baseURL string }{
		{"gap-compat-root", "http://fakes:9090/u/gap-compat-root"},
		{"gap-compat-v1", "http://fakes:9090/u/gap-compat-v1/v1"},
		{"gap-compat-slash", "http://fakes:9090/u/gap-compat-slash/v1/"},
	}
	var providers, aliases []map[string]any
	for _, c := range cases {
		providers = append(providers, map[string]any{
			"name": c.name, "kind": "openai-compatible", "base_url": c.baseURL, "allow_private_network": true,
			"keys": []map[string]string{{"name": "main", "value": c.name + "-key"}},
		})
		aliases = append(aliases, map[string]any{"name": c.name + "-alias", "kind": "chat", "members": []string{c.name + "/m1"}})
	}
	resetFakes(t)
	defer gapFixture(t, providers, aliases)()

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rep := awaitChat(t, chatMessage(c.name+"-alias", persian))
			wantHeader(t, rep, "X-Remora-Served-By", c.name+"/m1")
			if got := answerContent(t, rep); got != control.DefaultChatContent {
				t.Errorf("content %q, want %q", got, control.DefaultChatContent)
			}
			reqs := received(t, c.name, control.RouteOpenAIChat)
			if len(reqs) == 0 {
				t.Fatalf("%s got no chat request", c.name)
			}
			r := reqs[len(reqs)-1]
			if want := "/u/" + c.name + "/v1/chat/completions"; r.Path != want {
				t.Errorf("upstream path %q, want %q", r.Path, want)
			}
			if r.Header.Get("Authorization") != "Bearer "+c.name+"-key" {
				t.Errorf("upstream Authorization does not carry the provider's API key")
			}
		})
	}
}

// M10: max_tokens and max_output_tokens; reasoning effort passed through.
func TestMaxTokensAndReasoningEffort(t *testing.T) {
	t.Run("chat max_tokens", func(t *testing.T) {
		resetFakes(t)
		rep := chat(t, map[string]any{
			"model": "fast", "messages": []map[string]any{{"role": "user", "content": persian}},
			"max_tokens": 321, "reasoning_effort": "high",
		})
		wantStatus(t, rep, http.StatusOK)
		sent := sentFields(t, onlyRequest(t, "a", control.RouteOpenAIChat))
		limit := sent["max_tokens"]
		if limit == nil {
			limit = sent["max_completion_tokens"] // OpenAI's current name for the same cap
		}
		if string(limit) != "321" {
			t.Errorf("upstream got max_tokens %s, max_completion_tokens %s; want the cap 321; body %s",
				sent["max_tokens"], sent["max_completion_tokens"], onlyRequest(t, "a", control.RouteOpenAIChat).Body)
		}
		if string(sent["reasoning_effort"]) != `"high"` {
			t.Errorf("upstream reasoning_effort %s, want \"high\"", sent["reasoning_effort"])
		}
	})
	t.Run("chat max_output_tokens", func(t *testing.T) {
		resetFakes(t)
		rep := chat(t, map[string]any{
			"model": "fast", "messages": []map[string]any{{"role": "user", "content": persian}},
			"max_output_tokens": 654, "reasoning_effort": "low",
		})
		wantStatus(t, rep, http.StatusOK)
		r := onlyRequest(t, "a", control.RouteOpenAIChat)
		sent := sentFields(t, r)
		if string(sent["max_output_tokens"]) != "654" {
			t.Errorf("upstream max_output_tokens %s, want 654; body %s", sent["max_output_tokens"], r.Body)
		}
		if string(sent["reasoning_effort"]) != `"low"` {
			t.Errorf("upstream reasoning_effort %s, want \"low\"", sent["reasoning_effort"])
		}
	})
	t.Run("responses max_output_tokens", func(t *testing.T) {
		resetFakes(t)
		rep := doV1(t, http.MethodPost, "/v1/responses", keyFull, map[string]any{
			"model": "fast", "input": persian, "max_output_tokens": 456,
			"reasoning": map[string]any{"effort": "medium"},
		})
		wantStatus(t, rep, http.StatusOK)
		r := onlyRequest(t, "a", control.RouteOpenAIResponses)
		var sent struct {
			MaxOutputTokens json.RawMessage `json:"max_output_tokens"`
			Reasoning       struct {
				Effort string `json:"effort"`
			} `json:"reasoning"`
		}
		decodeJSON(t, r.Body, &sent)
		if string(sent.MaxOutputTokens) != "456" {
			t.Errorf("upstream max_output_tokens %s, want 456; body %s", sent.MaxOutputTokens, r.Body)
		}
		if sent.Reasoning.Effort != "medium" {
			t.Errorf("upstream reasoning.effort %q, want medium; body %s", sent.Reasoning.Effort, r.Body)
		}
	})
}

// N12: agent_message input items survive on /v1/responses.
func TestResponsesAgentMessage(t *testing.T) {
	resetFakes(t)
	// A multi-agent delegation item as coding agents send it; it is not a "message" and has no role.
	agentMessage := json.RawMessage(`{"type":"agent_message","id":"amsg_1","author":"/root","recipient":"/root/worker",` +
		`"content":[{"type":"input_text","text":` + strconv.Quote(persian) + `}]}`)
	input := json.RawMessage(`[{"type":"message","role":"user","content":[{"type":"input_text","text":"delegate"}]},` +
		string(agentMessage) + `]`)
	rep := doV1(t, http.MethodPost, "/v1/responses", keyFull, map[string]any{"model": "fast", "input": input})
	wantStatus(t, rep, http.StatusOK)
	wantHeader(t, rep, "X-Remora-Served-By", "up-a/m1")

	r := onlyRequest(t, "a", control.RouteOpenAIResponses)
	var sent struct {
		Input []json.RawMessage `json:"input"`
	}
	decodeJSON(t, r.Body, &sent)
	var found bool
	for _, item := range sent.Input {
		var head struct {
			Type string `json:"type"`
		}
		decodeJSON(t, item, &head)
		if head.Type != "agent_message" {
			continue
		}
		found = true
		if !jsonEqual(t, item, agentMessage) {
			t.Errorf("upstream agent_message %s, want %s", item, agentMessage)
		}
	}
	if !found {
		t.Errorf("upstream input has no agent_message item: %s", r.Body)
	}
	if len(sent.Input) != 2 {
		t.Errorf("upstream input has %d items, want 2: %s", len(sent.Input), r.Body)
	}
}

// S3: <provider>/<model> served directly, no fallback.
func TestProviderModelDirect(t *testing.T) {
	t.Run("served", func(t *testing.T) {
		resetFakes(t)
		rep := chat(t, chatMessage("up-b/m1", persian))
		wantStatus(t, rep, http.StatusOK)
		wantHeader(t, rep, "X-Remora-Served-By", "up-b/m1")
		wantHeader(t, rep, "X-Remora-Fallback", "false")
		var a chatAnswer
		decodeJSON(t, rep.Body, &a)
		if a.Model != "m1" {
			t.Errorf("model %q, want m1", a.Model)
		}
		if sent := sentFields(t, onlyRequest(t, "b", control.RouteOpenAIChat)); string(sent["model"]) != `"m1"` {
			t.Errorf("up-b got model %s, want \"m1\"", sent["model"])
		}
		for _, up := range []string{"a", "c"} {
			if n := len(received(t, up, control.RouteOpenAIChat)); n != 0 {
				t.Errorf("up-%s got %d chat requests, want 0", up, n)
			}
		}
	})
	t.Run("no fallback", func(t *testing.T) {
		resetFakes(t)
		// up-a/m1 is the first member of fast, whose fallback is up-b/m1; asked for directly, it has none.
		script(t, "a", control.RouteOpenAIChat, control.Script{
			Steps:  []control.Step{{Status: http.StatusTooManyRequests, Body: `{"error":{"type":"rate_limit_exceeded"}}`}},
			Repeat: true,
		})
		rep := chat(t, chatMessage("up-a/m1", persian))
		wantStatus(t, rep, http.StatusTooManyRequests)
		if n := len(received(t, "a", control.RouteOpenAIChat)); n < 1 {
			t.Errorf("up-a got %d chat requests, want at least 1", n)
		}
		for _, up := range []string{"b", "c"} {
			if n := len(received(t, up, control.RouteOpenAIChat)); n != 0 {
				t.Errorf("up-%s got %d chat requests, want 0: a direct model must not fall back", up, n)
			}
		}
	})
}
