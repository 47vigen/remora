package soniox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
)

// maxErrorBody caps how much of an error answer is read.
const maxErrorBody = 64 << 10

// APIError is a non-2xx answer from Soniox. Error() never contains the key.
type APIError struct {
	Op      string // the operation, such as "create transcription"
	Status  int
	Type    string // Soniox error_type
	Message string // Soniox message, redacted
}

// Error returns "soniox <op>: <status> <type>: <message>".
func (e *APIError) Error() string {
	return fmt.Sprintf("soniox %s: %d %s: %s", e.Op, e.Status, e.Type, e.Message)
}

// Client calls the Soniox async API. The key is passed per call, so one
// Client serves every account.
type Client struct {
	baseURL string
	hc      *http.Client
}

// NewClient returns a Client for baseURL (such as "https://api.soniox.com").
// A nil hc means http.DefaultClient.
func NewClient(baseURL string, hc *http.Client) *Client {
	if hc == nil {
		hc = http.DefaultClient
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), hc: hc}
}

// UploadFile streams body to POST /v1/files as the multipart field "file",
// after the field client_reference_id = ref.
func (c *Client) UploadFile(ctx context.Context, key, ref, filename string, body io.Reader) (File, error) {
	const op = "upload file"
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		pw.CloseWithError(writeUpload(mw, ref, filename, body))
	}()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/files", pr)
	if err != nil {
		_ = pr.CloseWithError(err)
		return File{}, fmt.Errorf("soniox %s: %w", op, err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	var f File
	err = c.do(req, key, op, &f)
	// Unblock the writer goroutine if the request ended before reading it all.
	_ = pr.CloseWithError(io.ErrClosedPipe)
	return f, err
}

func writeUpload(mw *multipart.Writer, ref, filename string, body io.Reader) error {
	if err := mw.WriteField("client_reference_id", ref); err != nil {
		return err
	}
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		return err
	}
	if _, err := io.Copy(fw, body); err != nil {
		return err
	}
	return mw.Close()
}

// CreateTranscription starts a transcription.
func (c *Client) CreateTranscription(ctx context.Context, key string, cr CreateRequest) (Transcription, error) {
	const op = "create transcription"
	b, err := json.Marshal(cr)
	if err != nil {
		return Transcription{}, fmt.Errorf("soniox %s: %w", op, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/transcriptions", bytes.NewReader(b))
	if err != nil {
		return Transcription{}, fmt.Errorf("soniox %s: %w", op, err)
	}
	req.Header.Set("Content-Type", "application/json")
	var tr Transcription
	err = c.do(req, key, op, &tr)
	return tr, err
}

// GetTranscription reads a transcription's status.
func (c *Client) GetTranscription(ctx context.Context, key, id string) (Transcription, error) {
	var tr Transcription
	err := c.get(ctx, key, "get transcription", "/v1/transcriptions/"+url.PathEscape(id), &tr)
	return tr, err
}

// GetTranscript fetches the transcript of a completed transcription.
func (c *Client) GetTranscript(ctx context.Context, key, id string) (Transcript, error) {
	var ts Transcript
	err := c.get(ctx, key, "get transcript", "/v1/transcriptions/"+url.PathEscape(id)+"/transcript", &ts)
	return ts, err
}

// DeleteFile deletes a file; a missing file counts as deleted.
func (c *Client) DeleteFile(ctx context.Context, key, id string) error {
	return c.delete(ctx, key, "delete file", "/v1/files/"+url.PathEscape(id))
}

// DeleteTranscription deletes a transcription; a missing one counts as deleted.
func (c *Client) DeleteTranscription(ctx context.Context, key, id string) error {
	return c.delete(ctx, key, "delete transcription", "/v1/transcriptions/"+url.PathEscape(id))
}

// ListFiles lists every file of the key's account, following
// next_page_cursor to the end.
func (c *Client) ListFiles(ctx context.Context, key string) ([]File, error) {
	var out []File
	err := c.list(ctx, key, "list files", "/v1/files", func(raw []byte) (*string, error) {
		var page struct {
			Files          []File  `json:"files"`
			NextPageCursor *string `json:"next_page_cursor"`
		}
		if err := json.Unmarshal(raw, &page); err != nil {
			return nil, err
		}
		out = append(out, page.Files...)
		return page.NextPageCursor, nil
	})
	return out, err
}

// ListTranscriptions lists every transcription of the key's account,
// following next_page_cursor to the end.
func (c *Client) ListTranscriptions(ctx context.Context, key string) ([]Transcription, error) {
	var out []Transcription
	err := c.list(ctx, key, "list transcriptions", "/v1/transcriptions", func(raw []byte) (*string, error) {
		var page struct {
			Transcriptions []Transcription `json:"transcriptions"`
			NextPageCursor *string         `json:"next_page_cursor"`
		}
		if err := json.Unmarshal(raw, &page); err != nil {
			return nil, err
		}
		out = append(out, page.Transcriptions...)
		return page.NextPageCursor, nil
	})
	return out, err
}

// maxPages stops a list whose cursor never ends.
const maxPages = 10000

func (c *Client) list(ctx context.Context, key, op, path string, page func([]byte) (*string, error)) error {
	cursor := ""
	for range maxPages {
		p := path
		if cursor != "" {
			p += "?cursor=" + url.QueryEscape(cursor)
		}
		var raw json.RawMessage
		if err := c.get(ctx, key, op, p, &raw); err != nil {
			return err
		}
		next, err := page(raw)
		if err != nil {
			return fmt.Errorf("soniox %s: decode: %w", op, err)
		}
		if next == nil || *next == "" {
			return nil
		}
		cursor = *next
	}
	return fmt.Errorf("soniox %s: more than %d pages", op, maxPages)
}

func (c *Client) get(ctx context.Context, key, op, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, http.NoBody)
	if err != nil {
		return fmt.Errorf("soniox %s: %w", op, err)
	}
	return c.do(req, key, op, out)
}

func (c *Client) delete(ctx context.Context, key, op, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.baseURL+path, http.NoBody)
	if err != nil {
		return fmt.Errorf("soniox %s: %w", op, err)
	}
	err = c.do(req, key, op, nil)
	if apiErr := (*APIError)(nil); errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound {
		return nil
	}
	return err
}

// do sends req with the bearer key. Any 2xx is success; the body is decoded into out when out is non-nil. A non-2xx
// answer is an *APIError with the key redacted.
func (c *Client) do(req *http.Request, key, op string, out any) error {
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("soniox %s: %w", op, redactErr(err, key))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return apiError(resp, key, op)
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("soniox %s: decode: %w", op, err)
	}
	return nil
}

func apiError(resp *http.Response, key, op string) *APIError {
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	var body struct {
		ErrorType    string `json:"error_type"`
		Message      string `json:"message"`
		ErrorMessage string `json:"error_message"`
	}
	e := &APIError{Op: op, Status: resp.StatusCode}
	if json.Unmarshal(raw, &body) == nil {
		e.Type = body.ErrorType
		e.Message = body.Message
		if e.Message == "" {
			e.Message = body.ErrorMessage
		}
	}
	if e.Message == "" {
		e.Message = strings.TrimSpace(string(raw))
	}
	if e.Type == "" {
		e.Type = "unknown_error"
	}
	e.Message = redact(e.Message, key)
	return e
}

// redact replaces every occurrence of key in s with "[REDACTED]".
func redact(s, key string) string {
	if key == "" {
		return s
	}
	return strings.ReplaceAll(s, key, "[REDACTED]")
}

// redactedError keeps the chain of an error while hiding the key in its text.
type redactedError struct {
	msg string
	err error
}

func (e *redactedError) Error() string { return e.msg }
func (e *redactedError) Unwrap() error { return e.err }

func redactErr(err error, key string) error {
	if key == "" || !strings.Contains(err.Error(), key) {
		return err
	}
	return &redactedError{msg: redact(err.Error(), key), err: err}
}
