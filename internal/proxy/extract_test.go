package proxy

import (
	"bytes"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newRequest(t *testing.T, contentType string, body []byte) *http.Request {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return req
}

func TestExtractModel(t *testing.T) {
	cases := []struct {
		name, contentType, body, want string
		err                           error
	}{
		{"json", "application/json", `{"model":"fast","messages":[]}`, "fast", nil},
		{"json no content type", "", `{"model":"memory/fast"}`, "memory/fast", nil},
		{"json with charset", "application/json; charset=utf-8", `{"model":"fast"}`, "fast", nil},
		{"json escaped key", "application/json", `{"model":"fast"}`, "fast", nil},
		{"json missing model", "application/json", `{"messages":[]}`, "", ErrNoModel},
		{"json model not string", "application/json", `{"model":7}`, "", ErrNoModel},
		{"json model null", "application/json", `{"model":null}`, "", ErrNoModel},
		{"json model empty", "application/json", `{"model":""}`, "", ErrNoModel},
		{"json model other case", "application/json", `{"Model":"fast"}`, "", ErrNoModel},
		{"json model duplicated", "application/json", `{"model":"fast","model":"other"}`, "", ErrNoModel},
		{"json model case duplicate", "application/json", `{"model":"fast","MODEL":"other"}`, "", ErrNoModel},
		{"fallbacks_field_rejected", "application/json", `{"model":"fast","fallbacks":["openrouter/x"]}`, "", ErrClientFallbacks},
		{"fallbacks other case rejected", "application/json", `{"model":"fast","Fallbacks":["openrouter/x"]}`, "", ErrClientFallbacks},
		{"fallbacks null rejected", "application/json", `{"fallbacks":null,"model":"fast"}`, "", ErrClientFallbacks},
		{"nested fallbacks allowed", "application/json", `{"model":"fast","metadata":{"fallbacks":1}}`, "fast", nil},
		{"not json", "application/json", `model=fast`, "", ErrNoModel},
		{"json array", "application/json", `[{"model":"fast"}]`, "", ErrNoModel},
		{"json trailing data", "application/json", `{"model":"fast"}{"model":"other"}`, "", ErrNoModel},
		{"json truncated", "application/json", `{"model":"fast",`, "", ErrNoModel},
		{"empty body", "application/json", ``, "", ErrNoModel},
		{"multipart without boundary", "multipart/form-data", `{"model":"fast"}`, "", ErrNoModel},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			model, body, err := ExtractModel(newRequest(t, c.contentType, []byte(c.body)))
			if !errors.Is(err, c.err) {
				t.Fatalf("err = %v, want %v", err, c.err)
			}
			if model != c.want {
				t.Fatalf("model = %q, want %q", model, c.want)
			}
			if err != nil {
				if body != nil {
					t.Fatalf("body = %v, want nil on error", body)
				}
				return
			}
			got, rerr := io.ReadAll(body)
			if rerr != nil {
				t.Fatalf("read body: %v", rerr)
			}
			if string(got) != c.body {
				t.Fatalf("body = %q, want %q", got, c.body)
			}
			if cerr := body.Close(); cerr != nil {
				t.Fatalf("close body: %v", cerr)
			}
		})
	}
}

type field struct {
	name, filename string
	value          []byte
}

func multipartBody(t *testing.T, fields []field) (body []byte, contentType string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for _, f := range fields {
		var (
			pw  io.Writer
			err error
		)
		if f.filename != "" {
			pw, err = w.CreateFormFile(f.name, f.filename)
		} else {
			pw, err = w.CreateFormField(f.name)
		}
		if err != nil {
			t.Fatalf("create part %q: %v", f.name, err)
		}
		if _, err := pw.Write(f.value); err != nil {
			t.Fatalf("write part %q: %v", f.name, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	return buf.Bytes(), w.FormDataContentType()
}

func TestExtractModel_MultipartModelAfterFile(t *testing.T) {
	audio := bytes.Repeat([]byte{0x00, 0xff, '\r', '\n', '-', '-', 'a'}, (5<<20)/7+1)
	raw, contentType := multipartBody(t, []field{
		{name: "file", filename: "audio.wav", value: audio},
		{name: "model", value: []byte("soniox/stt-async-v5")},
		{name: "enable_speaker_diarization", value: []byte("true")},
	})
	req := newRequest(t, contentType, raw)

	model, body, err := ExtractModel(req)
	if err != nil {
		t.Fatalf("ExtractModel: %v", err)
	}
	if model != "soniox/stt-async-v5" {
		t.Fatalf("model = %q, want %q", model, "soniox/stt-async-v5")
	}
	got, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !bytes.Equal(got, raw) {
		t.Fatalf("returned body differs from the sent body (%d vs %d bytes)", len(got), len(raw))
	}
	if ct := req.Header.Get("Content-Type"); ct != contentType {
		t.Fatalf("Content-Type = %q, want %q", ct, contentType)
	}
}

func TestExtractModel_MultipartErrors(t *testing.T) {
	cases := []struct {
		name   string
		fields []field
		err    error
	}{
		{"no model", []field{{name: "file", filename: "a.wav", value: []byte("x")}}, ErrNoModel},
		{"empty model", []field{{name: "model", value: nil}}, ErrNoModel},
		{"model only as file", []field{{name: "model", filename: "m.txt", value: []byte("fast")}}, ErrNoModel},
		{"model twice", []field{{name: "model", value: []byte("fast")}, {name: "model", value: []byte("other")}}, ErrNoModel},
		{"fallbacks", []field{{name: "model", value: []byte("fast")}, {name: "fallbacks", value: []byte("up-b/m1")}}, ErrClientFallbacks},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			raw, contentType := multipartBody(t, c.fields)
			model, body, err := ExtractModel(newRequest(t, contentType, raw))
			if !errors.Is(err, c.err) {
				t.Fatalf("err = %v, want %v", err, c.err)
			}
			if model != "" || body != nil {
				t.Fatalf("model = %q, body = %v; want empty on error", model, body)
			}
		})
	}
}

func TestExtractModel_MultipartTruncated(t *testing.T) {
	raw, contentType := multipartBody(t, []field{{name: "file", filename: "a.wav", value: []byte("abc")}})
	raw = raw[:len(raw)-10]
	if _, _, err := ExtractModel(newRequest(t, contentType, raw)); !errors.Is(err, ErrNoModel) {
		t.Fatalf("err = %v, want %v", err, ErrNoModel)
	}
}

func TestExtractModel_TooLarge(t *testing.T) {
	raw := make([]byte, MaxBodyBytes+1)
	copy(raw, `{"model":"fast","pad":"`)
	model, body, err := ExtractModel(newRequest(t, "application/json", raw))
	if !errors.Is(err, ErrBodyTooLarge) {
		t.Fatalf("err = %v, want %v", err, ErrBodyTooLarge)
	}
	if model != "" || body != nil {
		t.Fatalf("model = %q, body = %v; want empty on error", model, body)
	}
}

func TestExtractModel_AtCap(t *testing.T) {
	prefix, suffix := `{"model":"fast","pad":"`, `"}`
	raw := []byte(prefix + strings.Repeat("a", MaxBodyBytes-len(prefix)-len(suffix)) + suffix)
	model, body, err := ExtractModel(newRequest(t, "application/json", raw))
	if err != nil {
		t.Fatalf("ExtractModel: %v", err)
	}
	if model != "fast" {
		t.Fatalf("model = %q, want fast", model)
	}
	got, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !bytes.Equal(got, raw) {
		t.Fatal("returned body differs from the sent body")
	}
}

func TestExtractModel_PersianBytes(t *testing.T) {
	raw := []byte("{\"model\":\"memory/fast\",\"messages\":[{\"role\":\"user\",\"content\":\"این کار انجام می\u200cشود\"}]}")
	if !bytes.Contains(raw, []byte("\u200c")) {
		t.Fatal("test body lost its ZWNJ")
	}
	_, body, err := ExtractModel(newRequest(t, "application/json", raw))
	if err != nil {
		t.Fatalf("ExtractModel: %v", err)
	}
	got, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !bytes.Equal(got, raw) {
		t.Fatalf("body = %q, want %q", got, raw)
	}
}

func TestExtractModel_NoBody(t *testing.T) {
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/chat/completions", http.NoBody)
	req.Body = nil
	if _, _, err := ExtractModel(req); !errors.Is(err, ErrNoModel) {
		t.Fatalf("err = %v, want %v", err, ErrNoModel)
	}
}
