package soniox_test

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/47vigen/remora/test/fakes/soniox"
)

func newFake(t *testing.T) (*soniox.Fake, *httptest.Server) {
	t.Helper()
	f := soniox.New()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, srv
}

// call sends one request with bearer key and decodes a JSON answer into out
// (when non-nil). It returns the status code.
func call(t *testing.T, method, url, key, contentType string, body io.Reader, out any) int {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, url, body)
	if err != nil {
		t.Fatal(err)
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatalf("decode %s %s: %v (body %q)", method, url, err, raw)
		}
	}
	return resp.StatusCode
}

type fileResp struct {
	ID                string `json:"id"`
	Filename          string `json:"filename"`
	Size              int64  `json:"size"`
	CreatedAt         string `json:"created_at"`
	ClientReferenceID string `json:"client_reference_id"`
}

type txResp struct {
	ID              string `json:"id"`
	Status          string `json:"status"`
	FileID          string `json:"file_id"`
	ErrorType       string `json:"error_type"`
	ErrorMessage    string `json:"error_message"`
	AudioDurationMs int64  `json:"audio_duration_ms"`
}

func upload(t *testing.T, base, key, ref string) fileResp {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.WriteField("client_reference_id", ref); err != nil {
		t.Fatal(err)
	}
	fw, err := mw.CreateFormFile("file", "a.wav")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write([]byte("RIFF-audio"))
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	var f fileResp
	if code := call(t, http.MethodPost, base+"/v1/files", key, mw.FormDataContentType(), &buf, &f); code != http.StatusCreated {
		t.Fatalf("upload status = %d", code)
	}
	return f
}

func create(t *testing.T, base, key, body string) txResp {
	t.Helper()
	var tr txResp
	if code := call(t, http.MethodPost, base+"/v1/transcriptions", key, "application/json", strings.NewReader(body), &tr); code != http.StatusCreated {
		t.Fatalf("create status = %d", code)
	}
	return tr
}

func getTx(t *testing.T, base, key, id string) txResp {
	t.Helper()
	var tr txResp
	if code := call(t, http.MethodGet, base+"/v1/transcriptions/"+id, key, "", nil, &tr); code != http.StatusOK {
		t.Fatalf("get status = %d", code)
	}
	return tr
}

func TestFakeLifecycle(t *testing.T) {
	f, srv := newFake(t)
	toks := []soniox.Token{
		{Text: "می", StartMs: 0, EndMs: 100, Confidence: 0.9, Speaker: "1", Language: "fa"},
		{Text: "\u200cخواهم", StartMs: 100, EndMs: 300, Confidence: 0.9, Speaker: "1", Language: "fa"},
	}
	f.SetScript(soniox.Script{Tokens: toks, Text: "می\u200cخواهم", AudioDurationMs: 300})

	file := upload(t, srv.URL, "k", "remora:abc")
	if file.ID == "" || file.ClientReferenceID != "remora:abc" || file.Filename != "a.wav" || file.Size != 10 {
		t.Fatalf("file = %+v", file)
	}
	if _, err := time.Parse(time.RFC3339, file.CreatedAt); err != nil {
		t.Fatalf("created_at %q: %v", file.CreatedAt, err)
	}
	tr := create(t, srv.URL, "k", `{"model":"stt-async-v5","file_id":"`+file.ID+`","client_reference_id":"remora:abc"}`)
	if tr.Status != "queued" || tr.FileID != file.ID {
		t.Fatalf("created = %+v", tr)
	}
	if got := getTx(t, srv.URL, "k", tr.ID); got.Status != "completed" || got.AudioDurationMs != 300 {
		t.Fatalf("poll = %+v", got)
	}
	var ts struct {
		ID     string         `json:"id"`
		Text   string         `json:"text"`
		Tokens []soniox.Token `json:"tokens"`
	}
	if code := call(t, http.MethodGet, srv.URL+"/v1/transcriptions/"+tr.ID+"/transcript", "k", "", nil, &ts); code != http.StatusOK {
		t.Fatalf("transcript status = %d", code)
	}
	if ts.Text != "می\u200cخواهم" || len(ts.Tokens) != 2 || ts.Tokens[1] != toks[1] {
		t.Fatalf("transcript = %+v", ts)
	}
	if code := call(t, http.MethodDelete, srv.URL+"/v1/files/"+file.ID, "k", "", nil, nil); code != http.StatusNoContent {
		t.Fatalf("delete file = %d", code)
	}
	if code := call(t, http.MethodDelete, srv.URL+"/v1/transcriptions/"+tr.ID, "k", "", nil, nil); code != http.StatusNoContent {
		t.Fatalf("delete transcription = %d", code)
	}
	st := f.State()
	if len(st.Live) != 0 || len(st.Deleted) != 2 || st.Deleted[0].ID != file.ID || st.Deleted[1].ID != tr.ID {
		t.Fatalf("state = %+v", st)
	}
	if st.Polls != 1 {
		t.Fatalf("polls = %d, want 1", st.Polls)
	}
}

func TestFakeRequiresBearer(t *testing.T) {
	_, srv := newFake(t)
	var body struct {
		StatusCode int    `json:"status_code"`
		ErrorType  string `json:"error_type"`
		Message    string `json:"message"`
	}
	if code := call(t, http.MethodGet, srv.URL+"/v1/files", "", "", nil, &body); code != http.StatusUnauthorized {
		t.Fatalf("status = %d", code)
	}
	if body.StatusCode != http.StatusUnauthorized || body.ErrorType == "" || body.Message == "" {
		t.Fatalf("error body = %+v", body)
	}
}

func TestFakeProcessingFor(t *testing.T) {
	f, srv := newFake(t)
	f.SetScript(soniox.Script{ProcessingFor: 200 * time.Millisecond})
	file := upload(t, srv.URL, "k", "remora:p")
	tr := create(t, srv.URL, "k", `{"model":"m","file_id":"`+file.ID+`"}`)
	if got := getTx(t, srv.URL, "k", tr.ID); got.Status != "processing" {
		t.Fatalf("first poll = %q, want processing", got.Status)
	}
	time.Sleep(250 * time.Millisecond)
	if got := getTx(t, srv.URL, "k", tr.ID); got.Status != "completed" {
		t.Fatalf("later poll = %q, want completed", got.Status)
	}
}

func TestFakeFailWith(t *testing.T) {
	f, srv := newFake(t)
	f.SetScript(soniox.Script{FailWith: "audio_decode_failed"})
	file := upload(t, srv.URL, "k", "remora:e")
	tr := create(t, srv.URL, "k", `{"model":"m","file_id":"`+file.ID+`"}`)
	got := getTx(t, srv.URL, "k", tr.ID)
	if got.Status != "error" || got.ErrorMessage != "audio_decode_failed" || got.ErrorType == "" {
		t.Fatalf("poll = %+v", got)
	}
}

func TestFakeScriptedHTTPStatusEchoesAuth(t *testing.T) {
	f, srv := newFake(t)
	f.SetScript(soniox.Script{HTTPStatus: http.StatusTooManyRequests, EchoAuthInError: true})
	file := upload(t, srv.URL, "sk-x", "remora:h")
	var body struct {
		StatusCode int    `json:"status_code"`
		Message    string `json:"message"`
	}
	code := call(t, http.MethodPost, srv.URL+"/v1/transcriptions", "sk-x", "application/json",
		strings.NewReader(`{"model":"m","file_id":"`+file.ID+`"}`), &body)
	if code != http.StatusTooManyRequests || body.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, body %+v", code, body)
	}
	if !strings.Contains(body.Message, "sk-x") {
		t.Fatalf("message %q does not echo the credential", body.Message)
	}
}

func TestFakeDeleteProcessingIs409(t *testing.T) {
	f, srv := newFake(t)
	f.SetScript(soniox.Script{ProcessingFor: time.Hour})
	file := upload(t, srv.URL, "k", "remora:q")
	tr := create(t, srv.URL, "k", `{"model":"m","file_id":"`+file.ID+`"}`)
	var body struct {
		ErrorType string `json:"error_type"`
	}
	if code := call(t, http.MethodDelete, srv.URL+"/v1/transcriptions/"+tr.ID, "k", "", nil, &body); code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", code)
	}
	if body.ErrorType != "invalid_request" {
		t.Fatalf("error_type = %q", body.ErrorType)
	}
	if code := call(t, http.MethodDelete, srv.URL+"/v1/transcriptions/missing", "k", "", nil, nil); code != http.StatusNotFound {
		t.Fatalf("unknown id status = %d, want 404", code)
	}
}

func TestFakeDeleteFileFailsProcessingJob(t *testing.T) {
	f, srv := newFake(t)
	f.SetScript(soniox.Script{ProcessingFor: time.Hour})
	file := upload(t, srv.URL, "k", "remora:d")
	tr := create(t, srv.URL, "k", `{"model":"m","file_id":"`+file.ID+`"}`)
	if code := call(t, http.MethodDelete, srv.URL+"/v1/files/"+file.ID, "k", "", nil, nil); code != http.StatusNoContent {
		t.Fatalf("delete file = %d", code)
	}
	got := getTx(t, srv.URL, "k", tr.ID)
	if got.Status != "error" || got.ErrorType != "file_not_found" {
		t.Fatalf("after file delete = %+v", got)
	}
	if code := call(t, http.MethodDelete, srv.URL+"/v1/transcriptions/"+tr.ID, "k", "", nil, nil); code != http.StatusNoContent {
		t.Fatalf("delete failed job = %d", code)
	}
}

func TestFakeListPaginatesPerKey(t *testing.T) {
	f, srv := newFake(t)
	for _, k := range []string{"a", "a", "a", "b"} {
		f.Seed(soniox.Object{Kind: "file", Key: k, ClientReferenceID: "remora:" + k})
	}
	var ids []string
	cursor := ""
	pages := 0
	for {
		url := srv.URL + "/v1/files"
		if cursor != "" {
			url += "?cursor=" + cursor
		}
		var page struct {
			Files []struct {
				ID string `json:"id"`
			} `json:"files"`
			NextPageCursor *string `json:"next_page_cursor"`
		}
		if code := call(t, http.MethodGet, url, "a", "", nil, &page); code != http.StatusOK {
			t.Fatalf("list status = %d", code)
		}
		pages++
		for _, fl := range page.Files {
			ids = append(ids, fl.ID)
		}
		if page.NextPageCursor == nil || *page.NextPageCursor == "" {
			break
		}
		cursor = *page.NextPageCursor
	}
	if len(ids) != 3 || pages != 2 {
		t.Fatalf("key a saw %d files over %d pages, want 3 over 2", len(ids), pages)
	}
}

func TestFakeRecordsCreateBody(t *testing.T) {
	f, srv := newFake(t)
	file := upload(t, srv.URL, "k", "remora:r")
	body := `{"model":"m",  "file_id":"` + file.ID + `","enable_speaker_diarization":true,"context":"می` + "\u200c" + `خواهم"}`
	tr := create(t, srv.URL, "k", body)
	for _, o := range f.State().Live {
		if o.ID == tr.ID {
			if !bytes.Equal(o.CreateBody, []byte(body)) {
				t.Fatalf("CreateBody = %q, want %q", o.CreateBody, body)
			}
			if o.Kind != "transcription" || o.Key != "k" || o.FileID != file.ID {
				t.Fatalf("object = %+v", o)
			}
			return
		}
	}
	t.Fatal("transcription not live")
}

func TestFakeControlAPI(t *testing.T) {
	f, srv := newFake(t)
	script := `{"text":"hi","tokens":[{"text":"hi","start_ms":0,"end_ms":10,"confidence":1}],"audio_duration_ms":10,"processing_for":"1h"}`
	if code := call(t, http.MethodPut, srv.URL+"/_control/soniox/script", "", "application/json", strings.NewReader(script), nil); code != http.StatusNoContent {
		t.Fatalf("put script = %d", code)
	}
	seed := `{"kind":"transcription","key":"k","client_reference_id":"remora:s","status":"completed","created_at":"2026-01-01T00:00:00Z"}`
	if code := call(t, http.MethodPost, srv.URL+"/_control/soniox/seed", "", "application/json", strings.NewReader(seed), nil); code != http.StatusNoContent {
		t.Fatalf("seed = %d", code)
	}
	file := upload(t, srv.URL, "k", "remora:c")
	tr := create(t, srv.URL, "k", `{"model":"m","file_id":"`+file.ID+`"}`)
	if got := getTx(t, srv.URL, "k", tr.ID); got.Status != "processing" {
		t.Fatalf("scripted processing_for not applied: %+v", got)
	}
	var st soniox.State
	if code := call(t, http.MethodGet, srv.URL+"/_control/soniox/state", "", "", nil, &st); code != http.StatusOK {
		t.Fatalf("state = %d", code)
	}
	if len(st.Live) != 3 || st.Polls != 1 {
		t.Fatalf("state = %+v", st)
	}
	if st.Live[0].ClientReferenceID != "remora:s" || !st.Live[0].CreatedAt.Equal(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("seeded = %+v", st.Live[0])
	}
	if code := call(t, http.MethodPost, srv.URL+"/_control/soniox/reset", "", "", nil, nil); code != http.StatusNoContent {
		t.Fatalf("reset = %d", code)
	}
	if st := f.State(); len(st.Live) != 0 || len(st.Deleted) != 0 || st.Polls != 0 {
		t.Fatalf("after reset = %+v", st)
	}
}
