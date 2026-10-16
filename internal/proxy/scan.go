package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// span is raw[start:end], one JSON value; end == 0 means absent.
type span struct{ start, end int }

// bodySpans are where the parts of a request body the proxy reads sit, found in the one scan scanBody makes.
type bodySpans struct {
	model     span // the top-level "model" value, spelled exactly so
	messages  span // the last top-level key equal to "messages" under case folding, as encoding/json picks it
	input     span // the same for "input"
	firstUser span // the first element of messages, else of an input array, that is an object with "role": "user"
}

// scanBody finds the model the way the engine's decoder would see it and records the spans the proxy needs. The
// refusals are those of the decoder walk it replaced: a top-level "fallbacks" in any case, "model" in another case
// or given twice, a model that is not a non-empty string, and anything that is not one JSON object.
func scanBody(raw []byte) (model string, sp bodySpans, err error) {
	if !json.Valid(raw) {
		return "", sp, fmt.Errorf("%w: invalid JSON", ErrNoModel)
	}
	var (
		modelCount int
		modelErr   error
		fallbacks  bool
	)
	ok := scanObject(raw, func(key []byte, v span) bool {
		k := string(key)
		switch {
		case strings.EqualFold(k, fallbacksField):
			fallbacks = true
			return false
		case strings.EqualFold(k, modelField):
			modelCount++
			if k != modelField {
				modelErr = fmt.Errorf("%w: the field must be spelled %q", ErrNoModel, modelField)
				return true
			}
			if sp.model.end == 0 {
				sp.model = v
			}
			if err := json.Unmarshal(raw[v.start:v.end], &model); err != nil {
				modelErr = fmt.Errorf("%w: model is not a string", ErrNoModel)
			}
		case strings.EqualFold(k, "messages"):
			sp.messages = v
		case strings.EqualFold(k, "input"):
			sp.input = v
		}
		return true
	})
	switch {
	case fallbacks:
		return "", sp, ErrClientFallbacks
	case !ok:
		return "", sp, fmt.Errorf("%w: body is not a JSON object", ErrNoModel)
	case modelCount > 1:
		return "", sp, fmt.Errorf("%w: model given more than once", ErrNoModel)
	case modelErr != nil:
		return "", sp, modelErr
	case model == "":
		return "", sp, ErrNoModel
	}
	if sp.firstUser = firstUserIn(raw, sp.messages); sp.firstUser.end == 0 {
		sp.firstUser = firstUserIn(raw, sp.input)
	}
	return model, sp, nil
}

// firstUserIn is the first element of the array at arr that is an object whose top-level "role" is the string
// "user", or an absent span.
func firstUserIn(raw []byte, arr span) span {
	var found span
	if arr.end == 0 || raw[arr.start] != '[' {
		return found
	}
	forEachElement(raw, arr, func(el span) bool {
		if raw[el.start] != '{' {
			return true
		}
		scanObject(raw[el.start:el.end], func(key []byte, v span) bool {
			if string(key) != "role" {
				return true
			}
			var role string
			if json.Unmarshal(raw[el.start+v.start:el.start+v.end], &role) == nil && role == "user" {
				found = el
			}
			return false
		})
		return found.end == 0
	})
	return found
}

// scanObject calls fn with the decoded key and the value span of each member of the JSON object in b, in order,
// until fn returns false. It reports whether b is one well-formed object (as far as it read); it never panics and
// never reads past len(b), whatever b holds. A key without escapes aliases b.
func scanObject(b []byte, fn func(key []byte, v span) bool) bool {
	i := skipWS(b, 0)
	if i >= len(b) || b[i] != '{' {
		return false
	}
	i = skipWS(b, i+1)
	if i < len(b) && b[i] == '}' {
		return skipWS(b, i+1) == len(b)
	}
	for {
		if i >= len(b) || b[i] != '"' {
			return false
		}
		kEnd := skipString(b, i)
		if kEnd < 0 {
			return false
		}
		key := b[i+1 : kEnd-1]
		if bytes.IndexByte(key, '\\') >= 0 {
			var s string
			if json.Unmarshal(b[i:kEnd], &s) != nil {
				return false
			}
			key = []byte(s)
		}
		i = skipWS(b, kEnd)
		if i >= len(b) || b[i] != ':' {
			return false
		}
		i = skipWS(b, i+1)
		vEnd := skipValue(b, i)
		if vEnd < 0 {
			return false
		}
		if !fn(key, span{i, vEnd}) {
			return true
		}
		i = skipWS(b, vEnd)
		if i >= len(b) {
			return false
		}
		switch b[i] {
		case ',':
			i = skipWS(b, i+1)
		case '}':
			return skipWS(b, i+1) == len(b)
		default:
			return false
		}
	}
}

// forEachElement calls fn with each element of the JSON array at arr until fn returns false.
func forEachElement(b []byte, arr span, fn func(v span) bool) bool {
	end := arr.end
	i := skipWS(b[:end], arr.start+1)
	if i < end && b[i] == ']' {
		return true
	}
	for i < end {
		vEnd := skipValue(b[:end], i)
		if vEnd < 0 {
			return false
		}
		if !fn(span{i, vEnd}) {
			return true
		}
		i = skipWS(b[:end], vEnd)
		if i >= end {
			return false
		}
		if b[i] == ']' {
			return true
		}
		if b[i] != ',' {
			return false
		}
		i = skipWS(b[:end], i+1)
	}
	return false
}

func skipWS(b []byte, i int) int {
	for i < len(b) && (b[i] == ' ' || b[i] == '\t' || b[i] == '\n' || b[i] == '\r') {
		i++
	}
	return i
}

// skipString returns the index just past the string starting at b[i] == '"', or -1.
func skipString(b []byte, i int) int {
	i++
	for i < len(b) {
		j := bytes.IndexAny(b[i:], `"\`)
		if j < 0 {
			return -1
		}
		i += j
		if b[i] == '"' {
			return i + 1
		}
		i += 2 // a backslash and the byte it escapes
	}
	return -1
}

// skipValue returns the index just past the value starting at b[i], or -1. Containers are matched by depth with
// strings skipped; scalars run to the next delimiter. Syntax inside a value is not checked: scanBody runs json.Valid
// first.
func skipValue(b []byte, i int) int {
	if i >= len(b) {
		return -1
	}
	switch b[i] {
	case '"':
		return skipString(b, i)
	case '{', '[':
		depth := 0
		for i < len(b) {
			switch b[i] {
			case '"':
				if i = skipString(b, i); i < 0 {
					return -1
				}
				continue
			case '{', '[':
				depth++
			case '}', ']':
				if depth--; depth == 0 {
					return i + 1
				}
			}
			i++
		}
		return -1
	}
	start := i
	for i < len(b) && !isDelim(b[i]) {
		i++
	}
	if i == start {
		return -1
	}
	return i
}

func isDelim(c byte) bool {
	switch c {
	case ',', '}', ']', ' ', '\t', '\r', '\n':
		return true
	}
	return false
}
