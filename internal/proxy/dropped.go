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

// codexChatKept are the top-level Chat Completions keys the Codex translator carries besides the chosen token limit.
var codexChatKept = map[string]bool{
	"model": true, "messages": true, "stream": true, "stream_options": true, "reasoning_effort": true,
	"response_format": true, "text": true, "tool_choice": true, "tools": true, "service_tier": true,
	"parallel_tool_calls": true,
}

// codexResponsesDropped are the top-level Responses keys the Codex translator always deletes.
var codexResponsesDropped = []string{
	"temperature", "top_p", "truncation", "prompt_cache_options", "prompt_cache_retention", "user", "context_management",
}

// codexLimitKeys is the Chat Completions precedence order; Responses differs only for explicit max_output_tokens.
var codexLimitKeys = [...]string{"max_completion_tokens", "max_tokens", "max_output_tokens"}

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
		limit := codexNumericLimit(obj)
		for k := range obj {
			if !codexChatKept[k] && (limit == "" || k != limit) {
				out = append(out, k)
			}
		}
		out = appendIf(out, obj, "service_tier", func(v json.RawMessage) bool { return !codexKnownTier(v) })
		out = appendIf(out, obj, "parallel_tool_calls", notBool)
	case "/v1/responses":
		out = appendPresent(out, obj, codexResponsesDropped)
		limit := codexNumericLimit(obj)
		if v := obj["max_output_tokens"]; v != nil && (notJSON("null")(v) || limit == "") {
			limit = "max_output_tokens"
		}
		for _, k := range codexLimitKeys {
			if k != limit && obj[k] != nil {
				out = append(out, k)
			}
		}
		out = appendIf(out, obj, "service_tier", func(v json.RawMessage) bool { return !codexKnownTier(v) })
		out = appendIf(out, obj, "parallel_tool_calls", notBool)
		out = appendIf(out, obj, "store", notJSON("false"))
	case "/v1/messages":
		out = appendPresent(out, obj, []string{"temperature", "top_p"})
		// The front door maps positive max_tokens to Responses' max_output_tokens.
		out = appendIf(out, obj, "max_tokens", func(v json.RawMessage) bool {
			var n int
			return json.Unmarshal(v, &n) != nil || n <= 0
		})
	}
	if len(out) == 0 {
		return nil
	}
	sort.Strings(out)
	return out
}

// codexNumericLimit returns the first JSON number in translator order, excluding quoted numbers and null.
func codexNumericLimit(obj map[string]json.RawMessage) string {
	for _, k := range codexLimitKeys {
		if v := obj[k]; len(v) > 0 && (v[0] == '-' || v[0] >= '0' && v[0] <= '9') {
			return k
		}
	}
	return ""
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

// notBool reports a value other than true or false: the translator keeps an explicit false and forces everything
// else, absent included, to true (since v8.0.4x; before that it forced true over false too).
func notBool(v json.RawMessage) bool {
	return notJSON("true")(v) && notJSON("false")(v)
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
