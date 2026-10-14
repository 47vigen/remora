package proxy

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/47vigen/remora/internal/alias"
)

// shape is the error body format a route's clients expect.
type shape int

const (
	shapeOpenAI    shape = iota // {"error":{"message","type","code","param"}}
	shapeAnthropic              // {"type":"error","error":{"type","message"}}
	shapeTypeSafe               // {"detail":{"error_type","message"}}
)

// Error types remora uses in every shape.
const (
	typeAuthentication = "authentication_error"
	typePermission     = "permission_error"
	typeInvalidRequest = "invalid_request_error"
	typeNotFound       = "not_found_error"
	typeTooLarge       = "request_too_large"
	typeAPI            = "api_error"
)

// apiError is an error remora creates itself, before or instead of the front door's answer.
type apiError struct {
	status        int
	typ, code     string
	message       string
	retryAfterSec int // > 0 sets Retry-After
}

var (
	errNotFound        = apiError{status: http.StatusNotFound, typ: typeNotFound, code: "not_found", message: "Unknown path or method."}
	errInvalidKey      = apiError{status: http.StatusUnauthorized, typ: typeAuthentication, code: "invalid_api_key", message: "Missing, unknown or disabled API key."}
	errModelRequired   = apiError{status: http.StatusBadRequest, typ: typeInvalidRequest, code: "model_required", message: "The request must name a model in a string field \"model\"."}
	errClientFallbacks = apiError{status: http.StatusBadRequest, typ: typeInvalidRequest, code: "fallbacks_not_supported", message: "The \"fallbacks\" field is not supported; use an alias to define fallbacks."}
	errTooLarge        = apiError{status: http.StatusRequestEntityTooLarge, typ: typeTooLarge, code: "request_too_large", message: "The request body is larger than " + strconv.Itoa(MaxBodyBytes>>20) + " MiB."}
	errKeyNotReady     = apiError{status: http.StatusServiceUnavailable, typ: typeAPI, code: "key_not_ready", message: "This API key is not active yet; try again shortly.", retryAfterSec: 5}
	errNotReady        = apiError{status: http.StatusServiceUnavailable, typ: typeAPI, code: "not_ready", message: "remora is starting; try again shortly.", retryAfterSec: 5}
	errUpstream        = apiError{status: http.StatusBadGateway, typ: typeAPI, code: "upstream_unavailable", message: "The inference service is unavailable."}
	errInternal        = apiError{status: http.StatusInternalServerError, typ: typeAPI, code: "internal_error", message: "Internal error."}
)

func errModelNotAllowed(model string) apiError {
	return apiError{
		status: http.StatusForbidden, typ: typePermission, code: "model_not_allowed",
		message: "This API key may not use model " + strconv.Quote(model) + ".",
	}
}

// errAliasKind refuses an alias on an endpoint of another kind (an embeddings alias on chat completions).
func errAliasKind(model string, kind alias.Kind, path string) apiError {
	return apiError{
		status: http.StatusBadRequest, typ: typeInvalidRequest, code: "model_kind_mismatch",
		message: "Model " + strconv.Quote(model) + " is a " + string(kind) + " alias; it cannot be used on " + path + ".",
	}
}

// errUpstreamBody replaces an upstream error body too large to inspect, keeping its status.
func errUpstreamBody(status int) apiError {
	return apiError{status: status, typ: typeAPI, code: "upstream_error", message: "The upstream request failed."}
}

// body renders e in shape s.
func (e apiError) body(s shape) []byte {
	var v any
	switch s {
	case shapeAnthropic:
		v = anthropicError{Type: "error", Error: anthropicErrorBody{Type: e.typ, Message: e.message}}
	case shapeTypeSafe:
		v = typeSafeError{Detail: typeSafeErrorBody{ErrorType: e.typ, Message: e.message}}
	default:
		v = openAIError{Error: openAIErrorBody{
			Message: e.message, Type: jsonString(e.typ), Code: jsonString(e.code), Param: nil,
		}}
	}
	return marshal(v)
}

// write sends e in shape s.
func (e apiError) write(w http.ResponseWriter, s shape) {
	b := e.body(s)
	h := w.Header()
	h.Set("Content-Type", "application/json")
	h.Set("Content-Length", strconv.Itoa(len(b)))
	if e.retryAfterSec > 0 {
		h.Set("Retry-After", strconv.Itoa(e.retryAfterSec))
	}
	w.WriteHeader(e.status)
	_, _ = w.Write(b) //nolint:gosec // a JSON body remora encodes, served as application/json
}

// openAIError is the OpenAI error envelope. type, code and param are raw JSON so a missing value is null.
type openAIError struct {
	Error openAIErrorBody `json:"error"`
}

type openAIErrorBody struct {
	Message string          `json:"message"`
	Type    json.RawMessage `json:"type"`
	Code    json.RawMessage `json:"code"`
	Param   json.RawMessage `json:"param"`
}

// anthropicError is Anthropic's error envelope.
type anthropicError struct {
	Type  string             `json:"type"`
	Error anthropicErrorBody `json:"error"`
}

type anthropicErrorBody struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// typeSafeError is TypeSafe's error envelope.
type typeSafeError struct {
	Detail typeSafeErrorBody `json:"detail"`
}

type typeSafeErrorBody struct {
	ErrorType string `json:"error_type"`
	Message   string `json:"message"`
}

func jsonString(s string) json.RawMessage { return marshal(s) }

// marshal encodes a value of a fixed remora type, which always encodes.
func marshal(v any) []byte {
	b, err := encodeJSON(v)
	if err != nil {
		panic("proxy: encode error body: " + err.Error())
	}
	return b
}

// encodeJSON encodes v without HTML escaping and without a trailing newline.
func encodeJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}
