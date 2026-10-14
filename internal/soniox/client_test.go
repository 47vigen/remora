package soniox

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	fake "github.com/47vigen/remora/test/fakes/soniox"
)

func newFakeClient(t *testing.T) (*fake.Fake, *Client) {
	t.Helper()
	f := fake.New()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, NewClient(srv.URL, srv.Client())
}

func TestClientRoundTrip(t *testing.T) {
	f, c := newFakeClient(t)
	script := fake.Script{
		Text:            "Hello",
		AudioDurationMs: 200,
		Tokens: []fake.Token{
			{Text: "Hel", StartMs: 0, EndMs: 100, Confidence: 0.9, Speaker: "1", Language: "en"},
			{Text: "lo", StartMs: 100, EndMs: 200, Confidence: 0.8, Speaker: "1", Language: "en"},
		},
	}
	f.SetScript(script)
	ctx := t.Context()

	file, err := c.UploadFile(ctx, "k", "remora:abc", "a.wav", strings.NewReader("RIFF"))
	if err != nil {
		t.Fatal(err)
	}
	if file.ClientReferenceID != "remora:abc" || file.ID == "" || file.Filename != "a.wav" || file.CreatedAt.IsZero() {
		t.Fatalf("file = %+v", file)
	}
	tr, err := c.CreateTranscription(ctx, "k", CreateRequest{Model: "stt-async-v5", FileID: file.ID, ClientReferenceID: "remora:abc"})
	if err != nil {
		t.Fatal(err)
	}
	if tr.Status != "queued" || tr.FileID != file.ID || tr.ClientReferenceID != "remora:abc" {
		t.Fatalf("created = %+v", tr)
	}
	for i := 0; tr.Status != "completed"; i++ {
		if i > 10 {
			t.Fatalf("not completed: %+v", tr)
		}
		if tr, err = c.GetTranscription(ctx, "k", tr.ID); err != nil {
			t.Fatal(err)
		}
	}
	if tr.AudioDurationMs != 200 {
		t.Fatalf("audio_duration_ms = %d", tr.AudioDurationMs)
	}
	ts, err := c.GetTranscript(ctx, "k", tr.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ts.Text != "Hello" || len(ts.Tokens) != 2 {
		t.Fatalf("transcript = %+v", ts)
	}
	for i, want := range script.Tokens {
		got := ts.Tokens[i]
		if got.Text != want.Text || got.StartMs != want.StartMs || got.EndMs != want.EndMs ||
			got.Confidence != want.Confidence || got.Speaker != want.Speaker || got.Language != want.Language {
			t.Fatalf("token %d = %+v, want %+v", i, got, want)
		}
	}
	if err := c.DeleteFile(ctx, "k", file.ID); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteTranscription(ctx, "k", tr.ID); err != nil {
		t.Fatal(err)
	}
	if st := f.State(); len(st.Live) != 0 || len(st.Deleted) != 2 {
		t.Fatalf("state = %+v", st)
	}
}

func TestClientDeleteNotFoundIsNil(t *testing.T) {
	_, c := newFakeClient(t)
	if err := c.DeleteFile(t.Context(), "k", "missing"); err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}
	if err := c.DeleteTranscription(t.Context(), "k", "missing"); err != nil {
		t.Fatalf("DeleteTranscription: %v", err)
	}
}

func TestClientAPIError(t *testing.T) {
	f, c := newFakeClient(t)
	f.SetScript(fake.Script{HTTPStatus: http.StatusTooManyRequests})
	file, err := c.UploadFile(t.Context(), "k", "remora:x", "a.wav", strings.NewReader("x"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.CreateTranscription(t.Context(), "k", CreateRequest{Model: "m", FileID: file.ID})
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *APIError", err)
	}
	if apiErr.Status != http.StatusTooManyRequests || apiErr.Type != "rate_limit_exceeded" {
		t.Fatalf("apiErr = %+v", apiErr)
	}
	if !strings.HasPrefix(err.Error(), "soniox create transcription: 429 rate_limit_exceeded: ") {
		t.Fatalf("Error() = %q", err.Error())
	}
}

func TestClientUnauthorizedIsAPIError(t *testing.T) {
	_, c := newFakeClient(t)
	_, err := c.ListFiles(t.Context(), "")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusUnauthorized {
		t.Fatalf("err = %v", err)
	}
}

func TestClientErrorRedactsKey(t *testing.T) {
	f, c := newFakeClient(t)
	f.SetScript(fake.Script{HTTPStatus: http.StatusBadRequest, EchoAuthInError: true})
	const key = "sk-test-secret"
	file, err := c.UploadFile(t.Context(), key, "remora:x", "a.wav", strings.NewReader("x"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.CreateTranscription(t.Context(), key, CreateRequest{Model: "m", FileID: file.ID})
	if err == nil {
		t.Fatal("want an error")
	}
	if strings.Contains(err.Error(), key) {
		t.Fatalf("error leaks the key: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "[REDACTED]") {
		t.Fatalf("error not redacted: %q", err.Error())
	}
}

func TestClientListFollowsCursor(t *testing.T) {
	f, c := newFakeClient(t)
	for range 3 {
		f.Seed(fake.Object{Kind: "file", Key: "k", ClientReferenceID: "remora:a"})
		f.Seed(fake.Object{Kind: "transcription", Key: "k", ClientReferenceID: "remora:a"})
	}
	f.Seed(fake.Object{Kind: "file", Key: "other"})
	files, err := c.ListFiles(t.Context(), "k")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 {
		t.Fatalf("len(files) = %d, want 3", len(files))
	}
	txs, err := c.ListTranscriptions(t.Context(), "k")
	if err != nil {
		t.Fatal(err)
	}
	if len(txs) != 3 || txs[0].Status != "completed" {
		t.Fatalf("transcriptions = %+v", txs)
	}
}

func TestCreateRequestOmitsUnset(t *testing.T) {
	b, err := json.Marshal(CreateRequest{Model: "m", FileID: "f", ClientReferenceID: "r"})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"model":"m","file_id":"f","client_reference_id":"r"}`; string(b) != want {
		t.Fatalf("got %s, want %s", b, want)
	}
}

func TestRedact(t *testing.T) {
	if got := redact("a sk b sk", "sk"); got != "a [REDACTED] b [REDACTED]" {
		t.Fatalf("redact = %q", got)
	}
	if got := redact("abc", ""); got != "abc" {
		t.Fatalf("empty key: %q", got)
	}
}
