package soniox

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"slices"
	"strconv"
	"strings"
)

// Response formats the adapter can produce.
const (
	formatJSON    = "json"
	formatVerbose = "verbose_json"
	formatText    = "text"
)

// request is one parsed OpenAI transcription request.
type request struct {
	Create         CreateRequest // Model, hints, flags, context, translation; FileID/ClientReferenceID filled later
	ResponseFormat string        // "json" (default) | "verbose_json" | "text"
	Language       string        // the OpenAI language field, for the language fallback
}

// ParamError is a client mistake; the handler answers 400 with Error() as the
// message.
type ParamError struct{ Field, Reason string }

// reasonUnsupported marks a field the adapter does not know.
const reasonUnsupported = "unsupported"

// Error returns `unsupported parameter "<field>"` for an unknown field, else
// `invalid parameter "<field>": <reason>`.
func (e *ParamError) Error() string {
	if e.Reason == reasonUnsupported {
		return fmt.Sprintf("unsupported parameter %q", e.Field)
	}
	return fmt.Sprintf("invalid parameter %q: %s", e.Field, e.Reason)
}

// managedFields are Soniox fields the adapter owns: it creates and cleans up
// every object itself, and webhook secrets must never pass through a log.
var managedFields = map[string]bool{
	"audio_url":                 true,
	"file_id":                   true,
	"client_reference_id":       true,
	"webhook_url":               true,
	"webhook_auth_header_name":  true,
	"webhook_auth_header_value": true,
}

// parseParams maps the multipart value fields of an OpenAI transcription
// request to a Soniox create request. Every field is mapped or rejected;
// none is ignored. An empty value of a known field counts as unset (Bifrost
// drops empty fields too). Fields are checked in name order so the error is
// deterministic.
func parseParams(form *multipart.Form) (request, error) {
	req := request{ResponseFormat: formatJSON}
	var hints []string
	var prompt, context string

	names := make([]string, 0, len(form.Value))
	for name := range form.Value {
		names = append(names, name)
	}
	slices.Sort(names)

	for _, name := range names {
		values := form.Value[name]
		if managedFields[name] {
			return request{}, &ParamError{Field: name, Reason: "managed by remora"}
		}
		switch name {
		case "language_hints":
			for _, v := range values {
				h, err := parseHints(v)
				if err != nil {
					return request{}, &ParamError{Field: name, Reason: err.Error()}
				}
				hints = append(hints, h...)
			}
			continue
		case "timestamp_granularities[]", "timestamp_granularities":
			for _, v := range values {
				if v != "word" && v != "segment" {
					return request{}, &ParamError{Field: name, Reason: `each value must be "word" or "segment"`}
				}
			}
			continue
		case "temperature":
			return request{}, &ParamError{Field: name, Reason: "Soniox has no sampling temperature"}
		}

		known := name == "model" || name == "language" || name == "prompt" || name == "context" ||
			name == "response_format" || name == "translation" || name == "stream" ||
			name == "enable_speaker_diarization" || name == "enable_language_identification" ||
			name == "language_hints_strict"
		if !known {
			return request{}, &ParamError{Field: name, Reason: reasonUnsupported}
		}
		if len(values) > 1 {
			return request{}, &ParamError{Field: name, Reason: "must be given once"}
		}
		v := ""
		if len(values) == 1 {
			v = values[0]
		}
		if v == "" {
			continue
		}

		switch name {
		case "model":
			req.Create.Model = v
		case "language":
			req.Language = v
		case "prompt":
			prompt = v
		case "context":
			context = v
		case "response_format":
			if v != formatJSON && v != formatVerbose && v != formatText {
				return request{}, &ParamError{Field: name, Reason: "supported: json, verbose_json, text"}
			}
			req.ResponseFormat = v
		case "translation":
			if !isJSONObject(v) {
				return request{}, &ParamError{Field: name, Reason: "must be a JSON object"}
			}
			req.Create.Translation = json.RawMessage(v)
		case "stream":
			b, err := strconv.ParseBool(v)
			if err != nil {
				return request{}, &ParamError{Field: name, Reason: "must be a boolean"}
			}
			if b {
				return request{}, &ParamError{Field: name, Reason: "streaming is not supported"}
			}
		case "enable_speaker_diarization", "enable_language_identification", "language_hints_strict":
			b, err := strconv.ParseBool(v)
			if err != nil {
				return request{}, &ParamError{Field: name, Reason: "must be a boolean"}
			}
			switch name {
			case "enable_speaker_diarization":
				req.Create.EnableSpeakerDiarization = &b
			case "enable_language_identification":
				req.Create.EnableLanguageIdentification = &b
			default:
				req.Create.LanguageHintsStrict = &b
			}
		}
	}

	switch {
	case prompt != "" && context != "":
		return request{}, &ParamError{Field: "prompt", Reason: "use either prompt or context"}
	case prompt != "":
		b, err := json.Marshal(prompt)
		if err != nil {
			return request{}, &ParamError{Field: "prompt", Reason: err.Error()}
		}
		req.Create.Context = b
	case context != "":
		if strings.HasPrefix(strings.TrimSpace(context), "{") {
			if !isJSONObject(context) {
				return request{}, &ParamError{Field: "context", Reason: "a value starting with { must be a valid JSON object"}
			}
			req.Create.Context = json.RawMessage(context)
		} else {
			b, err := json.Marshal(context)
			if err != nil {
				return request{}, &ParamError{Field: "context", Reason: err.Error()}
			}
			req.Create.Context = b
		}
	}

	if req.Language != "" && !slices.Contains(hints, req.Language) {
		hints = append([]string{req.Language}, hints...)
	}
	if len(hints) > 0 {
		req.Create.LanguageHints = hints
	}
	if req.Create.Model == "" {
		return request{}, &ParamError{Field: "model", Reason: "required"}
	}
	return req, nil
}

// parseHints reads a JSON array of strings when v starts with "[", else a
// comma-separated list; entries are trimmed and empty ones dropped.
func parseHints(v string) ([]string, error) {
	var raw []string
	if strings.HasPrefix(strings.TrimSpace(v), "[") {
		if err := json.Unmarshal([]byte(v), &raw); err != nil {
			return nil, fmt.Errorf("must be a JSON array of strings or a comma-separated list")
		}
	} else {
		raw = strings.Split(v, ",")
	}
	var out []string
	for _, h := range raw {
		if h = strings.TrimSpace(h); h != "" {
			out = append(out, h)
		}
	}
	return out, nil
}

// isJSONObject reports whether v is valid JSON whose top level is an object.
func isJSONObject(v string) bool {
	b := bytes.TrimSpace([]byte(v))
	return len(b) > 0 && b[0] == '{' && json.Valid(b)
}
