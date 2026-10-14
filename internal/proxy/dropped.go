package proxy

import (
	"bytes"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
)

// headerDroppedParams names the request parameters a Codex member did not receive.
const headerDroppedParams = "X-Remora-Dropped-Params"

// codexServedPrefix is the served-by prefix of a Codex member.
const codexServedPrefix = "codex/"

// codexChatKept are the top-level chat-completions keys the OAuth engine's Codex translator carries into the
// request it builds; it builds that request from these keys only (C/internal/translator/codex/openai/
// chat-completions/codex_openai_request.go at v8.0.28).
var codexChatKept = map[string]bool{
	"model": true, "messages": true, "stream": true, "stream_options": true, "reasoning_effort": true,
	"response_format": true, "text": true, "tool_choice": true, "tools": true, "service_tier": true,
	"parallel_tool_calls": true,
}

// codexResponsesDropped are the top-level Responses keys the Codex translator deletes outright
// (C/internal/translator/codex/openai/responses/codex_openai-responses_request.go at v8.0.28).
var codexResponsesDropped = []string{
	"max_output_tokens", "max_completion_tokens", "temperature", "top_p", "truncation", "prompt_cache_options",
	"prompt_cache_retention", "user", "context_management",
}

// codexMessagesDropped are the Anthropic Messages keys that do not reach Codex: the front door turns the request
// into a Responses request, where they arrive as temperature, top_p and max_output_tokens and are deleted.
var codexMessagesDropped = []string{"temperature", "top_p", "max_tokens"}

// CodexDroppedParams returns the top-level keys of a client's body at path that the OAuth engine's Codex
// translator drops or overrides, sorted and deduplicated. It returns nil when there are none, when path is not a
// Codex-translated route, or when body is not a JSON object. The rules are checked against the engine on every
// engine bump (internal/engine/cliproxyapi/contract_test.go).
func CodexDroppedParams(path string, body []byte) []string {
	if path != "/v1/chat/completions" && path != "/v1/responses" && path != "/v1/messages" {
		return nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil || obj == nil {
		return nil
	}
	var out []string
	switch path {
	case "/v1/chat/completions":
		for k := range obj {
			if !codexChatKept[k] {
				out = append(out, k)
			}
		}
		out = appendIf(out, obj, "service_tier", func(v json.RawMessage) bool { return !codexKnownTier(v) })
		out = appendIf(out, obj, "parallel_tool_calls", notJSON("true"))
	case "/v1/responses":
		out = appendPresent(out, obj, codexResponsesDropped)
		out = appendIf(out, obj, "service_tier", func(v json.RawMessage) bool { return !codexKnownTier(v) })
		out = appendIf(out, obj, "parallel_tool_calls", notJSON("true"))
		out = appendIf(out, obj, "store", notJSON("false"))
	case "/v1/messages":
		out = appendPresent(out, obj, codexMessagesDropped)
	}
	if len(out) == 0 {
		return nil
	}
	sort.Strings(out)
	return out
}

// appendPresent appends every key of keys that obj has.
func appendPresent(out []string, obj map[string]json.RawMessage, keys []string) []string {
	for _, k := range keys {
		if _, ok := obj[k]; ok {
			out = append(out, k)
		}
	}
	return out
}

// appendIf appends key when obj has it and dropped reports its value as dropped or overridden.
func appendIf(out []string, obj map[string]json.RawMessage, key string, dropped func(json.RawMessage) bool) []string {
	if v, ok := obj[key]; ok && dropped(v) {
		out = append(out, key)
	}
	return out
}

// notJSON reports a value other than the JSON literal want, which the translator forces in its place.
func notJSON(want string) func(json.RawMessage) bool {
	return func(v json.RawMessage) bool { return !bytes.Equal(bytes.TrimSpace(v), []byte(want)) }
}

// codexKnownTier reports whether a service_tier value is one the Codex translator keeps: a string naming
// priority, fast or ultrafast, in any case.
func codexKnownTier(v json.RawMessage) bool {
	var s string
	if err := json.Unmarshal(v, &s); err != nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "priority", "fast", "ultrafast":
		return true
	}
	return false
}

// setDroppedParams adds x-remora-dropped-params when a Codex member served the request and the client's body
// carried parameters the Codex translator drops. It runs after the response hooks, so it sees the served-by they
// leave.
func setDroppedParams(h http.Header, info *reqInfo) {
	if !strings.HasPrefix(h.Get(headerServedBy), codexServedPrefix) || info.in == nil {
		return
	}
	if d := CodexDroppedParams(info.in.URL.Path, info.body); len(d) > 0 {
		h.Set(headerDroppedParams, strings.Join(d, ","))
	}
}
