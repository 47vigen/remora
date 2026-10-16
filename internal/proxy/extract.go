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
	model, raw, _, err := extractModel(r)
	if err != nil {
		return "", nil, err
	}
	return model, io.NopCloser(bytes.NewReader(raw)), nil
}

// extractModel is ExtractModel returning the held bytes themselves, so the proxy can hand them to response hooks
// without a second copy, and the spans of a JSON body (zero for multipart).
func extractModel(r *http.Request) (model string, raw []byte, sp bodySpans, err error) {
	if r.Body == nil || r.Body == http.NoBody {
		return "", nil, sp, fmt.Errorf("%w: empty body", ErrNoModel)
	}
	if r.ContentLength > MaxBodyBytes {
		return "", nil, sp, ErrBodyTooLarge
	}
	raw, err = readBody(r.Body, r.ContentLength)
	if err != nil {
		if mbe := (*http.MaxBytesError)(nil); errors.As(err, &mbe) {
			return "", nil, sp, ErrBodyTooLarge
		}
		return "", nil, sp, fmt.Errorf("proxy: read request body: %w", err)
	}
	if len(raw) > MaxBodyBytes {
		return "", nil, sp, ErrBodyTooLarge
	}

	mediaType, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaType == "multipart/form-data" {
		model, err = multipartModel(raw, params["boundary"])
	} else {
		model, sp, err = scanBody(raw)
	}
	if err != nil {
		return "", nil, bodySpans{}, err
	}
	return model, raw, sp, nil
}

// readBody reads at most MaxBodyBytes+1 bytes into one buffer sized from the declared length, so a large body is
// not copied through a doubling series of buffers.
func readBody(body io.Reader, declared int64) ([]byte, error) {
	var buf bytes.Buffer
	if declared > 0 {
		buf.Grow(int(declared) + bytes.MinRead)
	}
	_, err := buf.ReadFrom(io.LimitReader(body, MaxBodyBytes+1))
	return buf.Bytes(), err
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

// withModel returns body with its model replaced by model: the value of the top-level JSON "model" at sp.model, or
// the content of the multipart "model" field. Every other byte of a JSON body stays as it was. body and sp must come
// from extractModel.
func withModel(body []byte, sp bodySpans, contentType, model string) ([]byte, error) {
	mediaType, params, _ := mime.ParseMediaType(contentType)
	if mediaType == "multipart/form-data" {
		return multipartWithModel(body, params["boundary"], model)
	}
	if sp.model.end == 0 {
		return nil, ErrNoModel
	}
	v, err := json.Marshal(model)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(body)-(sp.model.end-sp.model.start)+len(v))
	out = append(out, body[:sp.model.start]...)
	out = append(out, v...)
	return append(out, body[sp.model.end:]...), nil
}

// multipartWithModel writes the parts again under the same boundary, so the request's Content-Type still fits.
func multipartWithModel(raw []byte, boundary, model string) ([]byte, error) {
	mr := multipart.NewReader(bytes.NewReader(raw), boundary)
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.SetBoundary(boundary); err != nil {
		return nil, fmt.Errorf("%w: invalid multipart boundary: %w", ErrNoModel, err)
	}
	for {
		part, err := mr.NextRawPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%w: invalid multipart body: %w", ErrNoModel, err)
		}
		w, err := mw.CreatePart(part.Header)
		if err != nil {
			return nil, err
		}
		if part.FormName() == modelField && part.FileName() == "" {
			_, err = io.WriteString(w, model)
		} else {
			_, err = io.Copy(w, part)
		}
		if err != nil {
			return nil, fmt.Errorf("%w: invalid multipart body: %w", ErrNoModel, err)
		}
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
