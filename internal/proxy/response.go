package proxy

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// Headers remora adds to /v1 responses.
const (
	headerAlias    = "X-Remora-Alias"
	headerServedBy = "X-Remora-Served-By"
	headerFallback = "X-Remora-Fallback"
)

// The front door's routing-info response headers (lib/responseheaders.go), read before they are removed.
const (
	headerRoutingProvider   = "X-Bifrost-Routing-Info-Provider"
	headerRoutingModel      = "X-Bifrost-Routing-Info-Model"
	headerRoutingIsFallback = "X-Bifrost-Routing-Info-Is-Fallback"
)

// nativeErrorKey marks the front door's own error envelope.
const nativeErrorKey = "is_bifrost_error"

// maxErrorBody is how much of an upstream error body remora reads to map and scrub it. A larger body is replaced.
const maxErrorBody = 1 << 20

// maxSSELine is the longest SSE line remora holds to inspect; a longer line is forwarded as it arrives, unread,
// unless it may be an error event, which is replaced with remora's own error.
const maxSSELine = 1 << 20

var (
	engineWords = regexp.MustCompile(`(?i)bifrost|cliproxyapi`)
	engineKeys  = regexp.MustCompile(`sk-bf-[A-Za-z0-9_-]+`)
)

// scrub replaces engine names with "remora" and engine key values with "[redacted]". The replacements are plain
// ASCII without quotes or backslashes, so a JSON document stays valid.
func scrub(b []byte) []byte {
	if !mayHoldEngineWord(b) {
		return b
	}
	b = engineKeys.ReplaceAll(b, []byte("[redacted]"))
	return engineWords.ReplaceAll(b, []byte("remora"))
}

func scrubString(s string) string {
	if !mayHoldEngineWord([]byte(s)) { // the conversion does not copy here
		return s
	}
	return string(scrub([]byte(s)))
}

// mayHoldEngineWord reports, without allocating, whether b may contain an engine name in any case or an engine key.
// A false answer is exact. Any non-ASCII byte answers true: the case-insensitive pattern also folds letters such
// as the Kelvin sign, so only the regexps can decide.
func mayHoldEngineWord(b []byte) bool {
	for i, c := range b {
		if c >= 0x80 {
			return true
		}
		switch c | 0x20 {
		case 'b':
			if hasFoldPrefix(b[i:], "bifrost") {
				return true
			}
		case 'c':
			if hasFoldPrefix(b[i:], "cliproxyapi") {
				return true
			}
		case 's':
			if bytes.HasPrefix(b[i:], []byte("sk-bf-")) {
				return true
			}
		}
	}
	return false
}

// hasFoldPrefix reports whether b starts with the lower-case ASCII word w, in any ASCII case.
func hasFoldPrefix(b []byte, w string) bool {
	if len(b) < len(w) {
		return false
	}
	for i := range len(w) {
		c := b[i]
		if 'A' <= c && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != w[i] {
			return false
		}
	}
	return true
}

// Scrub returns b with engine names replaced by remora's and engine keys redacted, for any text an operator or
// client reads (the CLI uses it on its error output).
func Scrub(b []byte) []byte { return scrub(b) }

// isEngineHeader reports whether a header name belongs to an engine.
func isEngineHeader(name string) bool {
	l := strings.ToLower(name)
	return strings.HasPrefix(l, "x-bf-") || strings.HasPrefix(l, "x-cpa-") || strings.Contains(l, "bifrost") ||
		strings.Contains(l, "cliproxyapi")
}

// cleanResponseHeaders removes engine headers, the Server header and the front door's CORS headers (they describe
// the engine's own UI, not remora's /v1), and scrubs every remaining value.
func cleanResponseHeaders(h http.Header) {
	for name, vals := range h {
		if isEngineHeader(name) || strings.EqualFold(name, "Server") ||
			strings.HasPrefix(strings.ToLower(name), "access-control-") {
			delete(h, name)
			continue
		}
		for i, v := range vals {
			vals[i] = scrubString(v)
		}
	}
}

// setRemoraHeaders maps the front door's routing info to remora's headers. Call it before cleanResponseHeaders.
func setRemoraHeaders(h http.Header, info *reqInfo) {
	provider, model := h.Get(headerRoutingProvider), h.Get(headerRoutingModel)
	fallback := strings.EqualFold(strings.TrimSpace(h.Get(headerRoutingIsFallback)), "true")
	cleanResponseHeaders(h)
	// These values are set after cleanResponseHeaders, so they are scrubbed here: a name may contain an engine word.
	if info.isAlias {
		h.Set(headerAlias, scrubString(info.model))
	}
	if provider != "" && model != "" {
		h.Set(headerServedBy, scrubString(provider+"/"+model))
	}
	h.Set(headerFallback, strconv.FormatBool(fallback))
}

func isEventStream(h http.Header) bool {
	mt, _, _ := mime.ParseMediaType(h.Get("Content-Type"))
	return mt == "text/event-stream"
}

// rewriteErrorBody reads an error body (at most maxErrorBody), maps the front door's native envelope to the
// route's error shape, scrubs engine words from any other body, and replaces a larger body with remora's own error.
func rewriteErrorBody(resp *http.Response, s shape) error {
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody+1))
	_ = resp.Body.Close()
	if err != nil {
		return fmt.Errorf("proxy: read upstream error body: %w", err)
	}
	var out []byte
	if len(raw) > maxErrorBody {
		out = errUpstreamBody(resp.StatusCode).body(s)
		resp.Header.Set("Content-Type", "application/json")
	} else if mapped, ok := mapNativeError(raw, s); ok {
		out = mapped
		resp.Header.Set("Content-Type", "application/json")
	} else {
		out = scrub(raw)
	}
	resp.Header.Del("Content-Encoding")
	resp.Header.Set("Content-Length", strconv.Itoa(len(out)))
	resp.ContentLength = int64(len(out))
	resp.Body = io.NopCloser(bytes.NewReader(out))
	return nil
}

// nativeError is the front door's error envelope (is_bifrost_error), read for remapping.
type nativeError struct {
	message          string
	typ, code, param json.RawMessage // raw JSON; empty when absent
}

// findNativeError reads the front door's error envelope from raw: either the whole document or, as in the front
// door's SSE error events, the value of "error".
func findNativeError(raw []byte) (nativeError, bool) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return nativeError{}, false
	}
	if _, ok := top[nativeErrorKey]; ok {
		return nativeFrom(top), true
	}
	if e, ok := top["error"]; ok {
		var inner map[string]json.RawMessage
		if err := json.Unmarshal(e, &inner); err == nil {
			if _, ok := inner[nativeErrorKey]; ok {
				return nativeFrom(inner), true
			}
		}
	}
	return nativeError{}, false
}

func nativeFrom(env map[string]json.RawMessage) nativeError {
	var e struct {
		Type    json.RawMessage `json:"type"`
		Code    json.RawMessage `json:"code"`
		Message json.RawMessage `json:"message"`
		Param   json.RawMessage `json:"param"`
	}
	if raw, ok := env["error"]; ok {
		_ = json.Unmarshal(raw, &e) // a non-object error leaves the fields empty
	}
	var msg string
	if err := json.Unmarshal(e.Message, &msg); err != nil || msg == "" {
		msg = errUpstreamBody(0).message
	}
	return nativeError{message: msg, typ: e.Type, code: e.Code, param: e.Param}
}

// openAI is the envelope's error in OpenAI's error object, null where the envelope has no value.
func (n nativeError) openAI() openAIErrorBody {
	return openAIErrorBody{Message: n.message, Type: orNull(n.typ), Code: orNull(n.code), Param: orNull(n.param)}
}

// typeName is the envelope's error type as a string, or api_error when it has none.
func (n nativeError) typeName() string {
	var t string
	if err := json.Unmarshal(n.typ, &t); err != nil || t == "" {
		return typeAPI
	}
	return t
}

// mapNativeError turns the front door's error envelope into the error shape s, scrubbed: OpenAI's
// {"error":{"message","type","code","param"}}, Anthropic's {"type":"error","error":{"type","message"}} or
// TypeSafe's {"detail":{"error_type","message"}}.
func mapNativeError(raw []byte, s shape) ([]byte, bool) {
	n, ok := findNativeError(raw)
	if !ok {
		return nil, false
	}
	var v any
	switch s {
	case shapeAnthropic:
		v = anthropicError{Type: "error", Error: anthropicErrorBody{Type: n.typeName(), Message: n.message}}
	case shapeTypeSafe:
		v = typeSafeError{Detail: typeSafeErrorBody{ErrorType: n.typeName(), Message: n.message}}
	default:
		v = openAIError{Error: n.openAI()}
	}
	out, err := encodeJSON(v)
	if err != nil {
		return nil, false
	}
	return scrub(out), true
}

func orNull(r json.RawMessage) json.RawMessage {
	if len(r) == 0 {
		return json.RawMessage("null")
	}
	return r
}

// rewriteSSELine returns line (with its line ending) unchanged, unless it is a data line carrying an error or the
// front door's "extra_fields": the front door's native error envelope becomes the route's error shape s, any other
// error event (a top-level "error" key or "type":"error") is scrubbed, and a top-level "extra_fields" is removed.
// Content is never touched.
func rewriteSSELine(line []byte, s shape) []byte {
	isErr := mayBeSSEError(line)
	extra := bytes.HasPrefix(line, []byte("data:")) && bytes.Contains(line, []byte(extraFieldsProbe))
	if !isErr && !extra {
		return line
	}
	payload := line[len("data:"):]
	prefix := line[:len("data:")]
	if len(payload) > 0 && payload[0] == ' ' {
		prefix, payload = line[:len("data: ")], payload[1:]
	}
	body := bytes.TrimRight(payload, "\r\n")
	ending := payload[len(body):]

	var (
		out    []byte
		mapped bool
	)
	if isErr {
		out, mapped = mapNativeError(body, s)
	}
	switch {
	case mapped:
	case isErr && isErrorEvent(body):
		out = stripExtraFields(scrub(body))
	case extra:
		if out = stripExtraFields(body); len(out) == len(body) {
			return line
		}
	default:
		return line
	}
	res := make([]byte, 0, len(prefix)+len(out)+len(ending))
	res = append(res, prefix...)
	res = append(res, out...)
	return append(res, ending...)
}

// markerOverlap is how many bytes of a forwarded chunk are kept to find an error marker split across chunks.
var markerOverlap = len(nativeErrorKey) - 1

// mayBeSSEErrorPart reports whether a piece of an over-long data line holds an error marker.
func mayBeSSEErrorPart(b []byte) bool {
	return bytes.Contains(b, []byte(`"error"`)) || bytes.Contains(b, []byte(nativeErrorKey))
}

// mayBeSSEError reports whether an SSE line is one rewriteSSELine could treat as an error event.
func mayBeSSEError(line []byte) bool {
	return bytes.HasPrefix(line, []byte("data:")) &&
		(bytes.Contains(line, []byte(`"error"`)) || bytes.Contains(line, []byte(nativeErrorKey)))
}

// isErrorEvent reports whether an event payload is an error: a JSON object with a top-level "error" key or
// "type":"error".
func isErrorEvent(body []byte) bool {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(body, &top); err != nil {
		return false
	}
	_, hasError := top["error"]
	var typ string
	_ = json.Unmarshal(top["type"], &typ)
	return hasError || typ == "error"
}

// sseScrubber forwards an SSE body line by line as each line arrives, rewriting only error events
// (rewriteSSELine). Nothing is held beyond the current line.
type sseScrubber struct {
	src         io.ReadCloser
	shape       shape
	br          *bufio.Reader
	line        []byte // the current line, being assembled
	pending     []byte // bytes ready for the caller
	passthrough bool   // inside an over-long line: forward it unread until its newline
	discard     bool   // inside an over-long line that may be an error: drop it until its newline
	tail        []byte // the last bytes forwarded in passthrough, so a marker split across chunks is still seen
	err         error
}

func newSSEScrubber(src io.ReadCloser, s shape) *sseScrubber {
	return &sseScrubber{src: src, shape: s, br: bufio.NewReaderSize(src, 8<<10)}
}

// Read hands over every complete line already buffered in one call, so an event costs one write to the client,
// not one per line; it waits for the upstream only when nothing is ready.
func (s *sseScrubber) Read(p []byte) (int, error) {
	for len(s.pending) == 0 {
		if s.err != nil {
			return 0, s.err
		}
		s.fill()
	}
	n := copy(p, s.pending)
	s.pending = s.pending[n:]
	for n < len(p) && len(s.pending) == 0 && s.err == nil && s.lineBuffered() {
		s.fill()
		m := copy(p[n:], s.pending)
		s.pending = s.pending[m:]
		n += m
	}
	return n, nil
}

// lineBuffered reports whether fill would return a whole line without waiting for the upstream.
func (s *sseScrubber) lineBuffered() bool {
	if s.passthrough || s.discard || len(s.line) > 0 {
		return false
	}
	buffered, _ := s.br.Peek(s.br.Buffered())
	return bytes.IndexByte(buffered, '\n') >= 0
}

// fill reads the next piece of the stream into pending (possibly nothing, while a long line is assembled).
func (s *sseScrubber) fill() {
	chunk, err := s.br.ReadSlice('\n')
	full := errors.Is(err, bufio.ErrBufferFull)
	if err != nil && !full {
		s.err = err
	}
	if !full && !s.passthrough && !s.discard && len(s.line) == 0 {
		// A whole line in one piece: forward it without a copy. chunk stays valid until the next read, and the next
		// read happens only once pending is drained.
		s.pending = rewriteSSELine(chunk, s.shape)
		return
	}
	s.line = append(s.line, chunk...) // chunk is only valid until the next read
	switch {
	case s.passthrough:
		// Keep looking for an error marker in the rest of the line, across chunk boundaries: once one shows up,
		// the line is cut there and remora's own error follows, so an engine error is never forwarded unscrubbed.
		s.tail = append(s.tail, s.line...)
		scan := s.tail
		if mayBeSSEErrorPart(scan) {
			s.pending = append([]byte("\n\n"), s.sseError()...)
			s.line, s.tail = nil, nil
			s.passthrough, s.discard = false, full
			break
		}
		s.tail = append([]byte(nil), scan[max(0, len(scan)-markerOverlap):]...)
		s.pending, s.line = s.line, nil
		s.passthrough = full
	case s.discard:
		s.line = nil
		s.discard = full
	case full && len(s.line) > maxSSELine && mayBeSSEError(s.line):
		// Too long to inspect, and it may carry an engine error: replace it rather than forward it unscrubbed.
		s.pending = s.sseError()
		s.line = nil
		s.discard = true
	case full && len(s.line) > maxSSELine:
		s.tail = append(s.tail[:0], s.line[max(0, len(s.line)-markerOverlap):]...)
		s.pending, s.line = s.line, nil
		s.passthrough = true
	case full:
		// keep assembling the line
	default:
		s.pending, s.line = rewriteSSELine(s.line, s.shape), s.line[:0]
	}
}

// sseError is remora's own error event, replacing a line too long to inspect.
func (s *sseScrubber) sseError() []byte {
	return append(append([]byte("data: "), errUpstreamBody(http.StatusBadGateway).body(s.shape)...), '\n')
}

func (s *sseScrubber) Close() error { return s.src.Close() }

// extraFieldsKey is the member the front door adds to every response object; it names internal providers and keys.
const extraFieldsKey = `"extra_fields"`

// extraFieldsProbe finds extraFieldsKey faster than the key itself: 'x' is rare in JSON, '"' is everywhere.
const extraFieldsProbe = `xtra_fields"`

// stripExtraFields removes the top-level "extra_fields" member, with one comma, from the JSON object in b. It returns
// b itself, without allocating, when there is none or b is not an object.
func stripExtraFields(b []byte) []byte {
	if !bytes.Contains(b, []byte(extraFieldsProbe)) {
		return b
	}
	i := skipWS(b, 0)
	if i >= len(b) || b[i] != '{' {
		return b
	}
	i = skipWS(b, i+1)
	prevEnd := -1 // just past the previous member's value
	for i < len(b) && b[i] == '"' {
		kEnd := skipString(b, i)
		if kEnd < 0 {
			return b
		}
		j := skipWS(b, kEnd)
		if j >= len(b) || b[j] != ':' {
			return b
		}
		vEnd := skipValue(b, skipWS(b, j+1))
		if vEnd < 0 {
			return b
		}
		next := skipWS(b, vEnd)
		if string(b[i:kEnd]) == extraFieldsKey {
			from, to := i, vEnd // the only member
			switch {
			case next < len(b) && b[next] == ',':
				to = skipWS(b, next+1) // the member and the comma after it
			case prevEnd >= 0:
				from = prevEnd // the last member and the comma before it
			}
			out := make([]byte, 0, len(b)-(to-from))
			out = append(out, b[:from]...)
			return append(out, b[to:]...)
		}
		if next >= len(b) || b[next] != ',' {
			return b
		}
		prevEnd, i = vEnd, skipWS(b, next+1)
	}
	return b
}

// stripJSONBody removes "extra_fields" from a successful JSON answer. A body over MaxBodyBytes passes untouched.
// shortcut: such a body keeps its extra_fields; stream the strip if one ever shows up.
func stripJSONBody(resp *http.Response) error {
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxBodyBytes+1))
	if err != nil {
		_ = resp.Body.Close()
		return fmt.Errorf("proxy: read upstream body: %w", err)
	}
	if len(data) > MaxBodyBytes {
		resp.Body = struct {
			io.Reader
			io.Closer
		}{io.MultiReader(bytes.NewReader(data), resp.Body), resp.Body}
		return nil
	}
	_ = resp.Body.Close()
	out := stripExtraFields(data)
	resp.Body = io.NopCloser(bytes.NewReader(out))
	resp.Header.Set("Content-Length", strconv.Itoa(len(out)))
	resp.ContentLength = int64(len(out))
	return nil
}

func isJSON(h http.Header) bool {
	mt, _, _ := mime.ParseMediaType(h.Get("Content-Type"))
	return mt == "application/json"
}
