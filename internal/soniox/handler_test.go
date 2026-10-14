package soniox

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	fake "github.com/47vigen/remora/test/fakes/soniox"
)

// waits records the durations the adapter waited between polls.
type waits struct {
	mu sync.Mutex
	d  []time.Duration
}

func (w *waits) add(d time.Duration) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.d = append(w.d, d)
}

func (w *waits) all() []time.Duration {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]time.Duration{}, w.d...)
}

func slogTo(w io.Writer) *slog.Logger {
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// newTestAdapter serves a fake and an Adapter in front of it whose waits
// return at once and are recorded.
func newTestAdapter(t *testing.T, cfg Config) (*Adapter, *fake.Fake, *waits) {
	t.Helper()
	f := fake.New()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	cfg.BaseURL = srv.URL
	if cfg.PollInterval == 0 {
		cfg.PollInterval = time.Second
	}
	a := New(cfg)
	w := &waits{}
	a.after = func(d time.Duration) <-chan time.Time {
		w.add(d)
		c := make(chan time.Time, 1)
		c <- time.Now()
		return c
	}
	return a, f, w
}

func audioRequest(ctx context.Context, t *testing.T, key string, fields map[string]string) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	fw, err := mw.CreateFormFile("file", "talk.wav")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write([]byte("RIFF-audio-bytes"))
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/v1/audio/transcriptions", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	return req
}

func postAudio(t *testing.T, h http.Handler, key string, fields map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, audioRequest(t.Context(), t, key, fields))
	return rec
}

type errorBody struct {
	Error struct {
		Message string  `json:"message"`
		Type    string  `json:"type"`
		Code    *string `json:"code"`
	} `json:"error"`
}

func decodeError(t *testing.T, rec *httptest.ResponseRecorder) errorBody {
	t.Helper()
	var e errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("decode error body %q: %v", rec.Body.String(), err)
	}
	if !strings.Contains(rec.Body.String(), `"code":null`) {
		t.Fatalf("error body %s has no null code", rec.Body.String())
	}
	return e
}

func twoSpeakers() fake.Script {
	return fake.Script{
		Text:            "سلام. می\u200cخواهم",
		AudioDurationMs: 3500,
		Tokens: []fake.Token{
			{Text: "سلام", StartMs: 0, EndMs: 400, Confidence: 0.9, Speaker: "1", Language: "fa"},
			{Text: ".", StartMs: 400, EndMs: 450, Confidence: 0.9, Speaker: "1", Language: "fa"},
			{Text: " می", StartMs: 1500, EndMs: 1700, Confidence: 0.9, Speaker: "2", Language: "fa"},
			{Text: "\u200cخواهم", StartMs: 1700, EndMs: 2100, Confidence: 0.9, Speaker: "2", Language: "fa"},
		},
	}
}

func assertCleanedUp(t *testing.T, f *fake.Fake, wantDeleted int) fake.State {
	t.Helper()
	st := f.State()
	if len(st.Live) != 0 || len(st.Deleted) != wantDeleted {
		t.Fatalf("live %d, deleted %d (want 0, %d): %+v", len(st.Live), len(st.Deleted), wantDeleted, st)
	}
	return st
}

func createBody(t *testing.T, st fake.State) map[string]any {
	t.Helper()
	for _, o := range append(st.Live, st.Deleted...) {
		if o.Kind == fake.KindTranscription {
			var m map[string]any
			if err := json.Unmarshal(o.CreateBody, &m); err != nil {
				t.Fatal(err)
			}
			return m
		}
	}
	t.Fatal("no transcription created")
	return nil
}

func TestHandlerVerboseJSON(t *testing.T) {
	a, f, _ := newTestAdapter(t, Config{})
	f.SetScript(twoSpeakers())
	rec := postAudio(t, a, "k", map[string]string{"model": "stt-async-v5", "response_format": "verbose_json", "enable_speaker_diarization": "true"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var v VerboseJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if len(v.Segments) != 2 || v.Segments[0].Speaker != "1" || v.Segments[1].Speaker != "2" {
		t.Fatalf("segments %+v", v.Segments)
	}
	if v.Segments[1].Text != "می\u200cخواهم" || v.Text != "سلام. می\u200cخواهم" {
		t.Fatalf("text not byte for byte: %+v", v)
	}
	if v.Duration != 3.5 || v.Language != "fa" || v.Task != "transcribe" || len(v.Words) != 2 {
		t.Fatalf("verbose %+v", v)
	}
	st := assertCleanedUp(t, f, 2)
	body := createBody(t, st)
	if body["enable_speaker_diarization"] != true || body["model"] != "stt-async-v5" {
		t.Fatalf("create body %v", body)
	}
	if ref, _ := body["client_reference_id"].(string); !strings.HasPrefix(ref, RefPrefix) || len(ref) != len(RefPrefix)+32 {
		t.Fatalf("client_reference_id %q", ref)
	}
	if st.Deleted[0].ClientReferenceID != body["client_reference_id"] {
		t.Fatalf("file ref %q differs from transcription ref", st.Deleted[0].ClientReferenceID)
	}
}

func TestHandlerJSONAndText(t *testing.T) {
	a, f, _ := newTestAdapter(t, Config{})
	f.SetScript(twoSpeakers())
	rec := postAudio(t, a, "k", map[string]string{"model": "m"})
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("json: %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if len(m) != 1 || m["text"] != "سلام. می\u200cخواهم" {
		t.Fatalf("json body %v", m)
	}
	rec = postAudio(t, a, "k", map[string]string{"model": "m", "response_format": "text"})
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Fatalf("text: %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if !bytes.Equal(rec.Body.Bytes(), []byte("سلام. می\u200cخواهم")) {
		t.Fatalf("text body %q", rec.Body.String())
	}
	assertCleanedUp(t, f, 4)
}

func TestHandlerForwardsContextAndHints(t *testing.T) {
	a, f, _ := newTestAdapter(t, Config{})
	rec := postAudio(t, a, "k", map[string]string{"model": "m", "context": `{"terms":["X"]}`, "language_hints": "fa,en"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	st := f.State()
	var body struct {
		Context       json.RawMessage `json:"context"`
		LanguageHints []string        `json:"language_hints"`
	}
	for _, o := range st.Deleted {
		if o.Kind == fake.KindTranscription {
			if err := json.Unmarshal(o.CreateBody, &body); err != nil {
				t.Fatal(err)
			}
		}
	}
	if string(body.Context) != `{"terms":["X"]}` || strings.Join(body.LanguageHints, ",") != "fa,en" {
		t.Fatalf("create body context %s hints %v", body.Context, body.LanguageHints)
	}
}

func TestHandlerPollIntervalClamped(t *testing.T) {
	for _, tc := range []struct {
		interval, want time.Duration
	}{
		{time.Millisecond, 500 * time.Millisecond},
		{5 * time.Minute, 60 * time.Second},
	} {
		a, f, w := newTestAdapter(t, Config{PollInterval: tc.interval})
		a.after = func(d time.Duration) <-chan time.Time {
			w.add(d)
			return time.After(10 * time.Millisecond)
		}
		f.SetScript(fake.Script{ProcessingFor: 35 * time.Millisecond})
		if rec := postAudio(t, a, "k", map[string]string{"model": "m"}); rec.Code != http.StatusOK {
			t.Fatalf("status %d: %s", rec.Code, rec.Body)
		}
		got := w.all()
		if len(got) < 2 {
			t.Fatalf("interval %v: only %d waits", tc.interval, len(got))
		}
		for _, d := range got {
			if d != tc.want {
				t.Fatalf("interval %v: waited %v, want %v", tc.interval, d, tc.want)
			}
		}
		if p := f.State().Polls; p != len(got)+1 {
			t.Fatalf("polls %d, waits %d", p, len(got))
		}
	}
}

func TestClampPoll(t *testing.T) {
	for in, want := range map[time.Duration]time.Duration{
		0: 500 * time.Millisecond, time.Second: time.Second, time.Hour: time.Minute,
	} {
		if got := clampPoll(in); got != want {
			t.Fatalf("clampPoll(%v) = %v, want %v", in, got, want)
		}
	}
}

func TestHandlerDeadline(t *testing.T) {
	f := fake.New()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	a := New(Config{BaseURL: srv.URL, Deadline: 50 * time.Millisecond})
	f.SetScript(fake.Script{ProcessingFor: time.Hour})
	start := time.Now()
	rec := postAudio(t, a, "k", map[string]string{"model": "m"})
	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if e := decodeError(t, rec); e.Error.Type != "timeout" || !strings.Contains(e.Error.Message, "did not complete within 50ms") {
		t.Fatalf("error %+v", e)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("deadline not honoured")
	}
	st := assertCleanedUp(t, f, 2)
	if st.Deleted[0].Kind != fake.KindFile {
		t.Fatalf("file not deleted first: %+v", st.Deleted)
	}
}

func TestHandlerJobErrorCleansUp(t *testing.T) {
	a, f, _ := newTestAdapter(t, Config{})
	f.SetScript(fake.Script{FailWith: "audio_decode_failed"})
	rec := postAudio(t, a, "k", map[string]string{"model": "m"})
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if e := decodeError(t, rec); e.Error.Type != "upstream_error" || !strings.Contains(e.Error.Message, "audio_decode_failed") ||
		!strings.HasPrefix(e.Error.Message, "transcription failed: ") {
		t.Fatalf("error %+v", e)
	}
	assertCleanedUp(t, f, 2)
}

func TestHandlerCreateRejectedCleansUpFile(t *testing.T) {
	a, f, _ := newTestAdapter(t, Config{})
	f.SetScript(fake.Script{HTTPStatus: http.StatusTooManyRequests})
	rec := postAudio(t, a, "k", map[string]string{"model": "m"})
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if e := decodeError(t, rec); e.Error.Type != "rate_limit_exceeded" || !strings.HasPrefix(e.Error.Message, "Soniox ") {
		t.Fatalf("error %+v", e)
	}
	st := assertCleanedUp(t, f, 1)
	if st.Deleted[0].Kind != fake.KindFile {
		t.Fatalf("deleted %+v", st.Deleted)
	}
}

func TestHandlerErrorStatusMapping(t *testing.T) {
	for _, tc := range []struct {
		upstream, want int
		typ            string
	}{
		{http.StatusPaymentRequired, http.StatusPaymentRequired, "insufficient_quota"},
		{http.StatusForbidden, http.StatusForbidden, "upstream_error"},
		{http.StatusUnprocessableEntity, http.StatusBadRequest, "upstream_error"},
		{http.StatusServiceUnavailable, http.StatusBadGateway, "upstream_error"},
	} {
		a, f, _ := newTestAdapter(t, Config{})
		f.SetScript(fake.Script{HTTPStatus: tc.upstream})
		rec := postAudio(t, a, "k", map[string]string{"model": "m"})
		if rec.Code != tc.want {
			t.Fatalf("upstream %d: status %d, want %d", tc.upstream, rec.Code, tc.want)
		}
		if e := decodeError(t, rec); e.Error.Type != tc.typ {
			t.Fatalf("upstream %d: type %q, want %q", tc.upstream, e.Error.Type, tc.typ)
		}
	}
}

func TestHandlerUpstreamUnreachableIs502(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	a := New(Config{BaseURL: url})
	rec := postAudio(t, a, "k", map[string]string{"model": "m"})
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if e := decodeError(t, rec); e.Error.Type != "upstream_error" || strings.Contains(e.Error.Message, url) {
		t.Fatalf("error %+v", e)
	}
}

func TestHandlerCleansUpWhenClientCancels(t *testing.T) {
	a, f, _ := newTestAdapter(t, Config{})
	f.SetScript(fake.Script{ProcessingFor: time.Hour})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	a.after = func(time.Duration) <-chan time.Time {
		cancel() // the client goes away after the first poll
		return make(chan time.Time)
	}
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		a.ServeHTTP(rec, audioRequest(ctx, t, "k", map[string]string{"model": "m"}))
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handler did not return within 1s of the cancel")
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("wrote %q to a gone client", rec.Body.String())
	}
	st := assertCleanedUp(t, f, 2)
	if st.Deleted[0].Kind != fake.KindFile || st.Deleted[1].Kind != fake.KindTranscription {
		t.Fatalf("deleted order %+v", st.Deleted)
	}
}

func TestHandlerCleanupOrderOnSuccess(t *testing.T) {
	a, f, _ := newTestAdapter(t, Config{})
	if rec := postAudio(t, a, "k", map[string]string{"model": "m"}); rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	st := assertCleanedUp(t, f, 2)
	if st.Deleted[0].Kind != fake.KindFile || st.Deleted[1].Kind != fake.KindTranscription || st.Deleted[1].FileID != st.Deleted[0].ID {
		t.Fatalf("deleted order %+v", st.Deleted)
	}
}

func TestHandlerParamErrorIs400(t *testing.T) {
	a, f, _ := newTestAdapter(t, Config{})
	rec := postAudio(t, a, "k", map[string]string{"model": "m", "foo": "1"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if e := decodeError(t, rec); e.Error.Message != `unsupported parameter "foo"` || e.Error.Type != "invalid_request_error" {
		t.Fatalf("error %+v", e)
	}
	if st := f.State(); len(st.Live)+len(st.Deleted) != 0 {
		t.Fatalf("fake saw an upload: %+v", st)
	}
}

func TestHandlerMissingFileIs400(t *testing.T) {
	a, f, _ := newTestAdapter(t, Config{})
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("model", "m")
	_ = mw.Close()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/audio/transcriptions", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer k")
	rec := httptest.NewRecorder()
	a.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if e := decodeError(t, rec); !strings.Contains(e.Error.Message, `"file"`) {
		t.Fatalf("error %+v", e)
	}
	if st := f.State(); len(st.Live)+len(st.Deleted) != 0 {
		t.Fatalf("fake saw an upload: %+v", st)
	}
}

func TestHandlerMissingKeyIs401(t *testing.T) {
	a, f, _ := newTestAdapter(t, Config{})
	rec := postAudio(t, a, "", map[string]string{"model": "m"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if e := decodeError(t, rec); e.Error.Type != "authentication_error" {
		t.Fatalf("error %+v", e)
	}
	if st := f.State(); len(st.Live)+len(st.Deleted) != 0 {
		t.Fatalf("fake saw an upload: %+v", st)
	}
}

func TestHandlerOtherRoutesAre404(t *testing.T) {
	a, _, _ := newTestAdapter(t, Config{})
	for _, r := range []*http.Request{
		httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/audio/transcriptions", http.NoBody),
		httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/audio/translations", http.NoBody),
	} {
		rec := httptest.NewRecorder()
		a.ServeHTTP(rec, r)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s %s: status %d", r.Method, r.URL.Path, rec.Code)
		}
		if e := decodeError(t, rec); e.Error.Type != "not_found" {
			t.Fatalf("error %+v", e)
		}
	}
}

func TestHandlerErrorRedactsKey(t *testing.T) {
	a, f, _ := newTestAdapter(t, Config{})
	var logs bytes.Buffer
	a.log = slogTo(&logs)
	f.SetScript(fake.Script{HTTPStatus: http.StatusBadRequest, EchoAuthInError: true})
	rec := postAudio(t, a, "sk-test-secret", map[string]string{"model": "m"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "sk-test-secret") || !strings.Contains(rec.Body.String(), "[REDACTED]") {
		t.Fatalf("body %s", rec.Body)
	}
	if strings.Contains(logs.String(), "sk-test-secret") {
		t.Fatalf("log leaks the key: %s", logs.String())
	}
}
