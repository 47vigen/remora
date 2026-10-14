// Package soniox is a scriptable fake of the Soniox async speech-to-text API
// (files and transcriptions). The fakes binary serves it for the e2e tests and
// unit tests import it directly, so both run against the same fake.
package soniox

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// maxBodyBytes caps an uploaded file or a request body.
const maxBodyBytes = 64 << 20

// pageSize is the fixed list page size, small so pagination is exercised.
const pageSize = 2

// Object kinds.
const (
	KindFile          = "file"
	KindTranscription = "transcription"
)

// Transcription statuses.
const (
	StatusQueued     = "queued"
	StatusProcessing = "processing"
	StatusCompleted  = "completed"
	StatusError      = "error"
)

// Token is one transcript token as Soniox returns it.
type Token struct {
	Text              string  `json:"text"`
	StartMs           int64   `json:"start_ms"`
	EndMs             int64   `json:"end_ms"`
	Confidence        float64 `json:"confidence"`
	Speaker           string  `json:"speaker,omitempty"`
	Language          string  `json:"language,omitempty"`
	TranslationStatus string  `json:"translation_status,omitempty"`
}

// Script is how the next transcriptions behave. A transcription takes the
// script that is set when it is created.
type Script struct {
	Tokens          []Token       `json:"tokens"`
	Text            string        `json:"text"`
	AudioDurationMs int64         `json:"audio_duration_ms"`
	ProcessingFor   time.Duration `json:"processing_for"`     // wall time in "processing" after create (JSON: Go duration string)
	FailWith        string        `json:"fail_with"`          // non-empty: job ends status "error" with this error_message
	HTTPStatus      int           `json:"http_status"`        // non-zero: create answers this status with Soniox error body
	EchoAuthInError bool          `json:"echo_auth_in_error"` // error message includes the Authorization header value
}

type scriptJSON struct {
	Tokens          []Token         `json:"tokens"`
	Text            string          `json:"text"`
	AudioDurationMs int64           `json:"audio_duration_ms"`
	ProcessingFor   json.RawMessage `json:"processing_for,omitempty"`
	FailWith        string          `json:"fail_with"`
	HTTPStatus      int             `json:"http_status"`
	EchoAuthInError bool            `json:"echo_auth_in_error"`
}

// MarshalJSON writes ProcessingFor as a Go duration string.
func (s Script) MarshalJSON() ([]byte, error) {
	d, err := json.Marshal(s.ProcessingFor.String())
	if err != nil {
		return nil, err
	}
	return json.Marshal(scriptJSON{
		Tokens: s.Tokens, Text: s.Text, AudioDurationMs: s.AudioDurationMs, ProcessingFor: d,
		FailWith: s.FailWith, HTTPStatus: s.HTTPStatus, EchoAuthInError: s.EchoAuthInError,
	})
}

// UnmarshalJSON reads ProcessingFor as a Go duration string ("250ms", "1h")
// or as a number of nanoseconds.
func (s *Script) UnmarshalJSON(b []byte) error {
	var raw scriptJSON
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	var d time.Duration
	if len(raw.ProcessingFor) > 0 && string(raw.ProcessingFor) != "null" {
		var str string
		if err := json.Unmarshal(raw.ProcessingFor, &str); err == nil {
			parsed, err := time.ParseDuration(str)
			if err != nil {
				return fmt.Errorf("processing_for: %w", err)
			}
			d = parsed
		} else {
			var n int64
			if err := json.Unmarshal(raw.ProcessingFor, &n); err != nil {
				return fmt.Errorf("processing_for: want a duration string: %w", err)
			}
			d = time.Duration(n)
		}
	}
	*s = Script{
		Tokens: raw.Tokens, Text: raw.Text, AudioDurationMs: raw.AudioDurationMs, ProcessingFor: d,
		FailWith: raw.FailWith, HTTPStatus: raw.HTTPStatus, EchoAuthInError: raw.EchoAuthInError,
	}
	return nil
}

// Object is one file or transcription the fake holds.
type Object struct {
	ID                string          `json:"id"`
	Kind              string          `json:"kind"` // "file" | "transcription"
	Key               string          `json:"key"`  // the bearer that created it
	ClientReferenceID string          `json:"client_reference_id"`
	Status            string          `json:"status"` // transcriptions only
	FileID            string          `json:"file_id"`
	CreatedAt         time.Time       `json:"created_at"`
	CreateBody        json.RawMessage `json:"create_body,omitempty"` // transcriptions only: the exact JSON body received
}

// State is a snapshot of the fake: live objects in creation order, deleted
// objects in deletion order, and the number of transcription polls.
type State struct {
	Live    []Object `json:"live"`
	Deleted []Object `json:"deleted"`
	Polls   int      `json:"polls"`
}

// record is an object plus what only the fake needs to know about it.
type record struct {
	Object
	filename     string
	size         int64
	model        string
	script       Script
	doneAt       time.Time // zero: the status never advances on its own
	errorType    string
	errorMessage string
}

// Fake is the Soniox API fake. It is safe for concurrent use.
type Fake struct {
	mu      sync.Mutex
	script  Script
	live    []*record
	deleted []Object
	polls   int
	now     func() time.Time
	handler http.Handler
}

// New returns an empty Fake.
func New() *Fake {
	f := &Fake{now: time.Now}
	f.handler = f.mux()
	return f
}

// SetScript sets how transcriptions created from now on behave.
func (f *Fake) SetScript(s Script) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.script = s
}

// Seed injects an object, such as a leftover from an earlier run. A missing ID
// gets a random one, a zero CreatedAt the current time, and a transcription
// without a status is "completed". A seeded object never changes status on
// its own.
func (f *Fake) Seed(o Object) {
	if o.ID == "" {
		o.ID = newID()
	}
	if o.CreatedAt.IsZero() {
		o.CreatedAt = f.now().UTC()
	}
	if o.Kind == KindTranscription && o.Status == "" {
		o.Status = StatusCompleted
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.live = append(f.live, &record{Object: o})
}

// State returns a snapshot of the fake.
func (f *Fake) State() State {
	f.mu.Lock()
	defer f.mu.Unlock()
	st := State{Live: make([]Object, 0, len(f.live)), Deleted: append([]Object{}, f.deleted...), Polls: f.polls}
	for _, r := range f.live {
		f.advance(r)
		st.Live = append(st.Live, r.Object)
	}
	return st
}

// Reset drops every object, the script and the poll count.
func (f *Fake) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.script = Script{}
	f.live = nil
	f.deleted = nil
	f.polls = 0
}

// ServeHTTP serves the Soniox API below /v1 and the control API below
// /_control/soniox.
func (f *Fake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.handler.ServeHTTP(w, r)
}

func (f *Fake) mux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/files", f.authed(f.uploadFile))
	mux.HandleFunc("GET /v1/files", f.authed(f.listFiles))
	mux.HandleFunc("DELETE /v1/files/{id}", f.authed(f.deleteFile))
	mux.HandleFunc("POST /v1/transcriptions", f.authed(f.createTranscription))
	mux.HandleFunc("GET /v1/transcriptions", f.authed(f.listTranscriptions))
	mux.HandleFunc("GET /v1/transcriptions/{id}", f.authed(f.getTranscription))
	mux.HandleFunc("DELETE /v1/transcriptions/{id}", f.authed(f.deleteTranscription))
	mux.HandleFunc("GET /v1/transcriptions/{id}/transcript", f.authed(f.getTranscript))
	f.controlRoutes(mux)
	return mux
}

type keyedHandler func(w http.ResponseWriter, r *http.Request, key string)

// authed answers 401 unless the request carries a non-empty bearer.
func (f *Fake) authed(h keyedHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || strings.TrimSpace(key) == "" {
			writeSonioxError(w, http.StatusUnauthorized, "unauthenticated", "Missing or invalid API key.")
			return
		}
		h(w, r, key)
	}
}

func (f *Fake) uploadFile(w http.ResponseWriter, r *http.Request, key string) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	mr, err := r.MultipartReader()
	if err != nil {
		writeSonioxError(w, http.StatusBadRequest, "invalid_request", "Expected a multipart/form-data body.")
		return
	}
	var (
		ref, filename string
		size          int64
		gotFile       bool
	)
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			writeSonioxError(w, http.StatusBadRequest, "invalid_request", "Malformed multipart body.")
			return
		}
		switch part.FormName() {
		case "file":
			gotFile = true
			filename = part.FileName()
			size, err = io.Copy(io.Discard, part)
		case "client_reference_id":
			var b []byte
			b, err = io.ReadAll(part)
			ref = string(b)
		default:
			_, err = io.Copy(io.Discard, part)
		}
		if err != nil {
			writeSonioxError(w, http.StatusBadRequest, "invalid_request", "Malformed multipart body.")
			return
		}
	}
	if !gotFile {
		writeSonioxError(w, http.StatusBadRequest, "invalid_request", "Missing file.")
		return
	}
	rec := &record{
		Object:   Object{ID: newID(), Kind: KindFile, Key: key, ClientReferenceID: ref, CreatedAt: f.now().UTC()},
		filename: filename,
		size:     size,
	}
	f.mu.Lock()
	f.live = append(f.live, rec)
	out := fileJSON(rec)
	f.mu.Unlock()
	writeJSON(w, http.StatusCreated, out)
}

func (f *Fake) createTranscription(w http.ResponseWriter, r *http.Request, key string) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		writeSonioxError(w, http.StatusBadRequest, "invalid_request", "Unreadable body.")
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	script := f.script
	if script.HTTPStatus != 0 {
		msg := "Scripted failure."
		if script.EchoAuthInError {
			msg += " Received Authorization: " + r.Header.Get("Authorization")
		}
		writeSonioxError(w, script.HTTPStatus, errorTypeFor(script.HTTPStatus), msg)
		return
	}
	var req struct {
		Model             string `json:"model"`
		FileID            string `json:"file_id"`
		ClientReferenceID string `json:"client_reference_id"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeSonioxError(w, http.StatusBadRequest, "invalid_request", "Invalid JSON body.")
		return
	}
	if req.Model == "" {
		writeSonioxError(w, http.StatusBadRequest, "invalid_request", "Missing model.")
		return
	}
	if f.find(KindFile, key, req.FileID) == nil {
		writeSonioxError(w, http.StatusBadRequest, "invalid_request", "File not found.")
		return
	}
	now := f.now().UTC()
	rec := &record{
		Object: Object{
			ID: newID(), Kind: KindTranscription, Key: key, ClientReferenceID: req.ClientReferenceID,
			Status: StatusQueued, FileID: req.FileID, CreatedAt: now, CreateBody: append(json.RawMessage{}, body...),
		},
		model:  req.Model,
		script: script,
		doneAt: now.Add(script.ProcessingFor),
	}
	f.live = append(f.live, rec)
	writeJSON(w, http.StatusCreated, txJSON(rec))
}

func (f *Fake) getTranscription(w http.ResponseWriter, r *http.Request, key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.polls++
	rec := f.find(KindTranscription, key, r.PathValue("id"))
	if rec == nil {
		writeNotFound(w)
		return
	}
	f.advance(rec)
	writeJSON(w, http.StatusOK, txJSON(rec))
}

func (f *Fake) getTranscript(w http.ResponseWriter, r *http.Request, key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rec := f.find(KindTranscription, key, r.PathValue("id"))
	if rec == nil {
		writeNotFound(w)
		return
	}
	f.advance(rec)
	if rec.Status != StatusCompleted {
		writeSonioxError(w, http.StatusConflict, "invalid_request", "Transcription is not completed.")
		return
	}
	tokens := rec.script.Tokens
	if tokens == nil {
		tokens = []Token{}
	}
	text := rec.script.Text
	if text == "" {
		var b strings.Builder
		for _, t := range tokens {
			b.WriteString(t.Text)
		}
		text = b.String()
	}
	writeJSON(w, http.StatusOK, struct {
		ID     string  `json:"id"`
		Text   string  `json:"text"`
		Tokens []Token `json:"tokens"`
	}{rec.ID, text, tokens})
}

func (f *Fake) deleteFile(w http.ResponseWriter, r *http.Request, key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rec := f.find(KindFile, key, r.PathValue("id"))
	if rec == nil {
		writeNotFound(w)
		return
	}
	for _, t := range f.live {
		if t.Kind != KindTranscription || t.Key != key || t.FileID != rec.ID {
			continue
		}
		f.advance(t)
		if t.Status == StatusQueued || t.Status == StatusProcessing {
			t.Status = StatusError
			t.errorType = "file_not_found"
			t.errorMessage = "The file was deleted before the transcription finished."
			t.doneAt = time.Time{}
		}
	}
	f.remove(rec)
	w.WriteHeader(http.StatusNoContent)
}

func (f *Fake) deleteTranscription(w http.ResponseWriter, r *http.Request, key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rec := f.find(KindTranscription, key, r.PathValue("id"))
	if rec == nil {
		writeNotFound(w)
		return
	}
	f.advance(rec)
	if rec.Status == StatusQueued || rec.Status == StatusProcessing {
		writeSonioxError(w, http.StatusConflict, "invalid_request", "Cannot delete transcriptions that are currently processing.")
		return
	}
	f.remove(rec)
	w.WriteHeader(http.StatusNoContent)
}

func (f *Fake) listFiles(w http.ResponseWriter, r *http.Request, key string) {
	f.list(w, r, key, KindFile, "files", func(rec *record) any { return fileJSON(rec) })
}

func (f *Fake) listTranscriptions(w http.ResponseWriter, r *http.Request, key string) {
	f.list(w, r, key, KindTranscription, "transcriptions", func(rec *record) any { return txJSON(rec) })
}

// list answers one page of the caller's objects of kind. The cursor is the
// offset of the page in the caller's list.
func (f *Fake) list(w http.ResponseWriter, r *http.Request, key, kind, field string, render func(*record) any) {
	offset := 0
	if c := r.URL.Query().Get("cursor"); c != "" {
		n, err := strconv.Atoi(c)
		if err != nil || n < 0 {
			writeSonioxError(w, http.StatusBadRequest, "invalid_request", "Invalid cursor.")
			return
		}
		offset = n
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var mine []*record
	for _, rec := range f.live {
		if rec.Kind == kind && rec.Key == key {
			f.advance(rec)
			mine = append(mine, rec)
		}
	}
	items := []any{}
	for i := offset; i < len(mine) && i < offset+pageSize; i++ {
		items = append(items, render(mine[i]))
	}
	var next *string
	if offset+pageSize < len(mine) {
		c := strconv.Itoa(offset + pageSize)
		next = &c
	}
	writeJSON(w, http.StatusOK, map[string]any{field: items, "next_page_cursor": next})
}

// advance moves a created transcription to its scripted end once its
// processing time is over. Callers hold f.mu.
func (f *Fake) advance(rec *record) {
	if rec.Kind != KindTranscription || rec.doneAt.IsZero() {
		return
	}
	if rec.Status != StatusQueued && rec.Status != StatusProcessing {
		return
	}
	if f.now().Before(rec.doneAt) {
		rec.Status = StatusProcessing
		return
	}
	rec.doneAt = time.Time{}
	if rec.script.FailWith != "" {
		rec.Status = StatusError
		rec.errorType = "transcription_failed"
		rec.errorMessage = rec.script.FailWith
		return
	}
	rec.Status = StatusCompleted
}

// find returns the live object of kind with id owned by key. Callers hold f.mu.
func (f *Fake) find(kind, key, id string) *record {
	for _, rec := range f.live {
		if rec.Kind == kind && rec.Key == key && rec.ID == id {
			return rec
		}
	}
	return nil
}

// remove moves rec from live to deleted. Callers hold f.mu.
func (f *Fake) remove(rec *record) {
	for i, r := range f.live {
		if r == rec {
			f.live = append(f.live[:i], f.live[i+1:]...)
			break
		}
	}
	f.deleted = append(f.deleted, rec.Object)
}

func fileJSON(rec *record) map[string]any {
	return map[string]any{
		"id":                  rec.ID,
		"filename":            rec.filename,
		"size":                rec.size,
		"created_at":          rec.CreatedAt.Format(time.RFC3339),
		"client_reference_id": nullable(rec.ClientReferenceID),
	}
}

func txJSON(rec *record) map[string]any {
	out := map[string]any{
		"id":                  rec.ID,
		"status":              rec.Status,
		"created_at":          rec.CreatedAt.Format(time.RFC3339),
		"model":               rec.model,
		"file_id":             nullable(rec.FileID),
		"client_reference_id": nullable(rec.ClientReferenceID),
		"error_type":          nullable(rec.errorType),
		"error_message":       nullable(rec.errorMessage),
		"audio_duration_ms":   nil,
	}
	if rec.Status == StatusCompleted {
		out["audio_duration_ms"] = rec.script.AudioDurationMs
	}
	return out
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func errorTypeFor(status int) string {
	switch status {
	case http.StatusUnauthorized:
		return "unauthenticated"
	case http.StatusPaymentRequired:
		return "insufficient_balance"
	case http.StatusTooManyRequests:
		return "rate_limit_exceeded"
	case http.StatusNotFound:
		return "not_found"
	}
	if status >= http.StatusInternalServerError {
		return "internal_error"
	}
	return "invalid_request"
}

func writeNotFound(w http.ResponseWriter) {
	writeSonioxError(w, http.StatusNotFound, "not_found", "Not found.")
}

// writeSonioxError writes Soniox's error body.
func writeSonioxError(w http.ResponseWriter, status int, typ, msg string) {
	writeJSON(w, status, map[string]any{
		"status_code":       status,
		"error_type":        typ,
		"message":           msg,
		"validation_errors": []any{},
		"request_id":        newID(),
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

// newID returns a random UUIDv4 string.
func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
