// Package proxy serves remora's /v1 surface in front of the inference engine.
package proxy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
)

// MaxBodyBytes is the largest request body remora accepts; it is the front door's own cap.
const MaxBodyBytes = 100 << 20

var (
	// ErrNoModel means the request names no usable model.
	ErrNoModel = errors.New("proxy: no model in request")
	// ErrClientFallbacks means the client sent its own fallbacks, which would route past the allowlist.
	ErrClientFallbacks = errors.New("proxy: fallbacks field is not supported")
	// ErrBodyTooLarge means the request body is larger than MaxBodyBytes.
	ErrBodyTooLarge = errors.New("proxy: request body too large")
)

const (
	modelField     = "model"
	fallbacksField = "fallbacks"
)

// ExtractModel reads the request body (up to MaxBodyBytes), finds the model the client asked for, and returns a
// reader over exactly the bytes it read. The model comes from a multipart/form-data field named "model" or from the
// top-level JSON object. A top-level "fallbacks" key is rejected: the engine would honour it and route past the
// allowlist. The request itself, its headers included, is not changed; on error the returned body is nil.
func ExtractModel(r *http.Request) (model string, body io.ReadCloser, err error) {
	if r.Body == nil || r.Body == http.NoBody {
		return "", nil, fmt.Errorf("%w: empty body", ErrNoModel)
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, MaxBodyBytes+1))
	if err != nil {
		if mbe := (*http.MaxBytesError)(nil); errors.As(err, &mbe) {
			return "", nil, ErrBodyTooLarge
		}
		return "", nil, fmt.Errorf("proxy: read request body: %w", err)
	}
	if len(raw) > MaxBodyBytes {
		return "", nil, ErrBodyTooLarge
	}

	mediaType, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaType == "multipart/form-data" {
		model, err = multipartModel(raw, params["boundary"])
	} else {
		model, err = jsonModel(raw)
	}
	if err != nil {
		return "", nil, err
	}
	return model, io.NopCloser(bytes.NewReader(raw)), nil
}

// jsonModel walks the top-level object key by key, so duplicate keys and case variants are seen: the engine's JSON
// decoder matches field names case-insensitively, so "Fallbacks" or a second "MODEL" would reach it as well.
func jsonModel(raw []byte) (string, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return "", fmt.Errorf("%w: body is not a JSON object", ErrNoModel)
	}
	var (
		model      string
		modelCount int
		modelErr   error
	)
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return "", fmt.Errorf("%w: invalid JSON: %w", ErrNoModel, err)
		}
		key, ok := tok.(string)
		if !ok {
			return "", fmt.Errorf("%w: invalid JSON object key", ErrNoModel)
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return "", fmt.Errorf("%w: invalid JSON: %w", ErrNoModel, err)
		}
		switch {
		case strings.EqualFold(key, fallbacksField):
			return "", ErrClientFallbacks
		case strings.EqualFold(key, modelField):
			modelCount++
			if key != modelField {
				modelErr = fmt.Errorf("%w: the field must be spelled %q", ErrNoModel, modelField)
				continue
			}
			if err := json.Unmarshal(value, &model); err != nil {
				modelErr = fmt.Errorf("%w: model is not a string", ErrNoModel)
			}
		}
	}
	if tok, err := dec.Token(); err != nil || tok != json.Delim('}') {
		return "", fmt.Errorf("%w: invalid JSON object", ErrNoModel)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("%w: data after the JSON object", ErrNoModel)
	}
	switch {
	case modelCount > 1:
		return "", fmt.Errorf("%w: model given more than once", ErrNoModel)
	case modelErr != nil:
		return "", modelErr
	case model == "":
		return "", ErrNoModel
	}
	return model, nil
}

// multipartModel reads every part the way the engine's form parser does (mime/multipart, non-file parts are
// values), so the whole body is checked: "model" may come after a large file part, and "fallbacks" anywhere.
func multipartModel(raw []byte, boundary string) (string, error) {
	if boundary == "" {
		return "", fmt.Errorf("%w: multipart body without boundary", ErrNoModel)
	}
	mr := multipart.NewReader(bytes.NewReader(raw), boundary)
	var (
		model      string
		modelCount int
	)
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("%w: invalid multipart body: %w", ErrNoModel, err)
		}
		name := part.FormName()
		switch {
		case strings.EqualFold(name, fallbacksField):
			_ = part.Close()
			return "", ErrClientFallbacks
		case name == modelField && part.FileName() == "":
			value, err := io.ReadAll(part)
			if err != nil {
				_ = part.Close()
				return "", fmt.Errorf("%w: invalid multipart body: %w", ErrNoModel, err)
			}
			model = string(value)
			modelCount++
		}
		if err := part.Close(); err != nil {
			return "", fmt.Errorf("%w: invalid multipart body: %w", ErrNoModel, err)
		}
	}
	switch {
	case modelCount > 1:
		return "", fmt.Errorf("%w: model given more than once", ErrNoModel)
	case model == "":
		return "", ErrNoModel
	}
	return model, nil
}
