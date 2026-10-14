//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/47vigen/remora/test/fakes/control"
	sxfake "github.com/47vigen/remora/test/fakes/soniox"
)

// keySpeech is the client key compose.yaml sets for the speech key of testdata/remora.yaml; it may only use soniox/*.
const keySpeech = "rk-e2e-speech-key"

// sonioxKey is the provider key compose.yaml sets for the soniox provider. The fake records the bearer that created
// every object, which proves the front door forwarded the provider key to the adapter and the adapter to Soniox.
const sonioxKey = "fake-soniox-key"

// sonioxModel is the model clients ask for: the soniox provider, a Soniox async model.
const sonioxModel = "soniox/stt-async-v5"

// sonioxRef is the client_reference_id prefix the adapter gives every object it creates.
const sonioxRef = "remora:"

// Segment texts of testdata/two-speakers.json; each holds a ZWNJ (U+200C) and is compared byte for byte.
const (
	twoSpeakersSeg0 = "سلام می\u200cشود"
	twoSpeakersSeg1 = "نمی\u200cخواهم"
)

// field is one multipart value field, in the order it is sent.
type field struct{ name, value string }

// verboseJSON is the part of the verbose_json answer the tests read.
type verboseJSON struct {
	Text     string `json:"text"`
	Language string `json:"language"`
	Words    []struct {
		Word    string `json:"word"`
		Speaker string `json:"speaker"`
	} `json:"words"`
	Segments []struct {
		ID      int    `json:"id"`
		Speaker string `json:"speaker"`
		Text    string `json:"text"`
	} `json:"segments"`
}

// sonioxCall sends a request to the Soniox fake's control API and returns the body, failing unless the status is want.
func sonioxCall(t *testing.T, method, path string, body []byte, want int) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, fakesURL(t, path), bytes.NewReader(body))
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("%s %s: read body: %v", method, path, err)
	}
	if resp.StatusCode != want {
		t.Fatalf("%s %s: status %d, want %d; body %s", method, path, resp.StatusCode, want, out)
	}
	return out
}

// sonioxSetup resets the fakes (the control server's recordings and the Soniox fake) and sets the Soniox script.
func sonioxSetup(t *testing.T, s sxfake.Script) {
	t.Helper()
	resetFakes(t)
	sonioxCall(t, http.MethodPost, "/_control/soniox/reset", nil, http.StatusNoContent)
	sonioxCall(t, http.MethodPut, "/_control/soniox/script", []byte(mustJSON(t, s)), http.StatusNoContent)
}

// twoSpeakers reads testdata/two-speakers.json.
func twoSpeakers(t *testing.T) sxfake.Script {
	t.Helper()
	b, err := os.ReadFile("testdata/two-speakers.json")
	if err != nil {
		t.Fatalf("read script: %v", err)
	}
	var s sxfake.Script
	decodeJSON(t, b, &s)
	if !strings.Contains(s.Text, "\u200c") {
		t.Fatal("testdata/two-speakers.json lost its ZWNJ")
	}
	return s
}

// sonioxState reads the Soniox fake's state.
func sonioxState(t *testing.T) sxfake.State {
	t.Helper()
	var st sxfake.State
	decodeJSON(t, sonioxCall(t, http.MethodGet, "/_control/soniox/state", nil, http.StatusOK), &st)
	return st
}

// waitSoniox polls the Soniox fake every 500ms until ok holds or timeout passes, and returns the last state. The
// adapter cleans up before it answers, so the state is usually final at once; polling keeps a slow cleanup from
// failing the test.
func waitSoniox(t *testing.T, timeout time.Duration, ok func(sxfake.State) bool) sxfake.State {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		st := sonioxState(t)
		if ok(st) || time.Now().After(deadline) {
			return st
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func noneLive(st sxfake.State) bool { return len(st.Live) == 0 }

// transcribe posts testdata/short.wav and fields to remora's /v1/audio/transcriptions with the speech key and reads
// the whole answer. Like doV1, it fails the test when the answer names an engine or carries an engine key.
func transcribe(t *testing.T, timeout time.Duration, fields ...field) reply {
	t.Helper()
	audio, err := os.ReadFile("testdata/short.wav")
	if err != nil {
		t.Fatalf("read audio: %v", err)
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for _, f := range fields {
		if err := mw.WriteField(f.name, f.value); err != nil {
			t.Fatalf("write field %s: %v", f.name, err)
		}
	}
	fw, err := mw.CreateFormFile("file", "short.wav")
	if err != nil {
		t.Fatalf("create file part: %v", err)
	}
	if _, err := fw.Write(audio); err != nil {
		t.Fatalf("write file part: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}

	const path = "/v1/audio/transcriptions"
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, remoraURL(t, path), &body)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+keySpeech)
	resp, err := (&http.Client{Timeout: timeout}).Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("POST %s: read body: %v", path, err)
	}
	rep := reply{Status: resp.StatusCode, Header: resp.Header, Body: out}
	checkLeaks(t, "POST "+path, rep)
	return rep
}

// errorMessage reads error.message from an OpenAI error body.
func errorMessage(t *testing.T, rep reply) string {
	t.Helper()
	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	decodeJSON(t, rep.Body, &body)
	return body.Error.Message
}

// wantCleanedUp checks that the fake holds nothing live and that the job deleted exactly one file and one
// transcription, both created with the provider key and tagged with the same remora client_reference_id. It returns
// the deleted transcription.
func wantCleanedUp(t *testing.T) sxfake.Object {
	t.Helper()
	st := waitSoniox(t, 5*time.Second, noneLive)
	if len(st.Live) != 0 {
		t.Errorf("live objects after the answer: %+v", st.Live)
	}
	if len(st.Deleted) != 2 {
		t.Fatalf("deleted %d objects, want 2 (one file, one transcription): %+v", len(st.Deleted), st.Deleted)
	}
	var file, tx *sxfake.Object
	for i := range st.Deleted {
		switch o := &st.Deleted[i]; o.Kind {
		case sxfake.KindFile:
			file = o
		case sxfake.KindTranscription:
			tx = o
		}
	}
	if file == nil || tx == nil {
		t.Fatalf("deleted objects %+v, want one file and one transcription", st.Deleted)
	}
	if !strings.HasPrefix(tx.ClientReferenceID, sonioxRef) || file.ClientReferenceID != tx.ClientReferenceID {
		t.Errorf("client_reference_id: file %q, transcription %q; want the same %q... value",
			file.ClientReferenceID, tx.ClientReferenceID, sonioxRef)
	}
	if tx.FileID != file.ID {
		t.Errorf("transcription file_id %q, want the deleted file %q", tx.FileID, file.ID)
	}
	for _, o := range []*sxfake.Object{file, tx} {
		if o.Key != sonioxKey {
			t.Errorf("%s created with key %q, want the provider key %q", o.Kind, o.Key, sonioxKey)
		}
	}
	return *tx
}

// TestTranscriptionSpeakersSurvive proves a diarized transcription keeps its speakers through the front door (the
// engine patch), keeps ZWNJ byte for byte, forwards the Soniox-only fields to the create request, and leaves nothing
// behind at Soniox.
func TestTranscriptionSpeakersSurvive(t *testing.T) {
	sonioxSetup(t, twoSpeakers(t))

	rep := transcribe(t, 60*time.Second,
		field{"model", sonioxModel},
		field{"response_format", "verbose_json"},
		field{"enable_speaker_diarization", "true"},
		field{"language", "fa"},
		field{"context", `{"terms":["remora"]}`},
		field{"language_hints", "fa,en"},
	)
	wantStatus(t, rep, http.StatusOK)

	var v verboseJSON
	decodeJSON(t, rep.Body, &v)
	if len(v.Segments) != 2 {
		t.Fatalf("got %d segments, want 2; body %s", len(v.Segments), rep.Body)
	}
	for i, want := range []struct {
		speaker, text string
	}{{"1", twoSpeakersSeg0}, {"2", twoSpeakersSeg1}} {
		s := v.Segments[i]
		if s.ID != i {
			t.Errorf("segment %d: id %d, want %d", i, s.ID, i)
		}
		if s.Speaker != want.speaker {
			t.Errorf("segment %d: speaker %q, want %q (the front door dropped the speaker)", i, s.Speaker, want.speaker)
		}
		if !bytes.Equal([]byte(s.Text), []byte(want.text)) {
			t.Errorf("segment %d: text %q (% x), want %q (% x)", i, s.Text, s.Text, want.text, want.text)
		}
	}
	if len(v.Words) == 0 {
		t.Fatalf("no words; body %s", rep.Body)
	}
	for i, w := range v.Words {
		if w.Speaker == "" {
			t.Errorf("word %d %q has no speaker", i, w.Word)
		}
	}

	tx := wantCleanedUp(t)
	var create struct {
		EnableSpeakerDiarization any             `json:"enable_speaker_diarization"`
		Context                  json.RawMessage `json:"context"`
		LanguageHints            []string        `json:"language_hints"`
	}
	decodeJSON(t, tx.CreateBody, &create)
	if create.EnableSpeakerDiarization != true {
		t.Errorf("create enable_speaker_diarization = %#v, want the boolean true; body %s",
			create.EnableSpeakerDiarization, tx.CreateBody)
	}
	if len(create.Context) == 0 || !jsonEqual(t, create.Context, []byte(`{"terms":["remora"]}`)) {
		t.Errorf("create context = %s, want {\"terms\":[\"remora\"]}", create.Context)
	}
	if !slices.Equal(create.LanguageHints, []string{"fa", "en"}) {
		t.Errorf("create language_hints = %q, want [fa en]", create.LanguageHints)
	}
}

// TestTranscriptionLongJob proves a job that runs for more than two minutes is not cut by any timeout between the
// client and the adapter, and that the adapter keeps polling until it ends.
func TestTranscriptionLongJob(t *testing.T) {
	const processing = 130 * time.Second
	s := twoSpeakers(t)
	s.ProcessingFor = processing
	sonioxSetup(t, s)

	start := time.Now()
	rep := transcribe(t, 5*time.Minute, field{"model", sonioxModel}, field{"response_format", "json"})
	elapsed := time.Since(start)
	wantStatus(t, rep, http.StatusOK)
	var body struct {
		Text string `json:"text"`
	}
	decodeJSON(t, rep.Body, &body)
	if !bytes.Equal([]byte(body.Text), []byte(s.Text)) {
		t.Errorf("text %q, want %q", body.Text, s.Text)
	}
	if elapsed < processing {
		t.Errorf("answered after %s, before the job could end (%s)", elapsed, processing)
	}

	st := waitSoniox(t, 5*time.Second, noneLive)
	if len(st.Live) != 0 {
		t.Errorf("live objects after the answer: %+v", st.Live)
	}
	// 130s at the 500ms poll interval is about 260 polls.
	if st.Polls < 200 {
		t.Errorf("the adapter polled %d times, want at least 200", st.Polls)
	}
}

// TestTranscriptionJobErrorCleansUp proves a job that fails at Soniox answers an OpenAI error that names no engine
// and still deletes what it created.
func TestTranscriptionJobErrorCleansUp(t *testing.T) {
	s := twoSpeakers(t)
	s.FailWith = "audio could not be decoded"
	sonioxSetup(t, s)

	rep := transcribe(t, 60*time.Second, field{"model", sonioxModel})
	if rep.Status < 400 {
		t.Fatalf("status %d, want an error; body %s", rep.Status, rep.Body)
	}
	if strings.Contains(strings.ToLower(string(rep.Body)), "bifrost") {
		t.Errorf("error body names the engine: %s", rep.Body)
	}
	if msg := errorMessage(t, rep); msg == "" {
		t.Errorf("no error.message; body %s", rep.Body)
	}
	wantCleanedUp(t)
}

// TestTranscriptionUnknownParam proves an unknown field is rejected with a 400 naming it before anything reaches
// Soniox.
func TestTranscriptionUnknownParam(t *testing.T) {
	sonioxSetup(t, twoSpeakers(t))

	rep := transcribe(t, 60*time.Second, field{"model", sonioxModel}, field{"foo", "1"})
	wantStatus(t, rep, http.StatusBadRequest)
	if msg := errorMessage(t, rep); !strings.Contains(msg, `unsupported parameter "foo"`) {
		t.Errorf("error.message %q, want it to contain %q; body %s", msg, `unsupported parameter "foo"`, rep.Body)
	}
	if st := sonioxState(t); len(st.Live) != 0 || len(st.Deleted) != 0 {
		t.Errorf("the fake saw objects: live %+v, deleted %+v", st.Live, st.Deleted)
	}
	if got := received(t, "sx", control.RouteSonioxFileCreate); len(got) != 0 {
		t.Errorf("the fake got %d uploads, want none", len(got))
	}
}

// TestTranscriptionSweep proves the sweeper deletes remora-tagged leftovers older than an hour with the provider
// key, and leaves young remora objects and objects of other tools alone.
func TestTranscriptionSweep(t *testing.T) {
	sonioxSetup(t, sxfake.Script{})
	now := time.Now().UTC()
	seed := func(o sxfake.Object) {
		t.Helper()
		o.Key = sonioxKey
		sonioxCall(t, http.MethodPost, "/_control/soniox/seed", []byte(mustJSON(t, o)), http.StatusNoContent)
	}
	seed(sxfake.Object{
		ID: "file-remora-old", Kind: sxfake.KindFile, ClientReferenceID: "remora:old",
		CreatedAt: now.Add(-2 * time.Hour),
	})
	seed(sxfake.Object{
		ID: "tx-remora-old", Kind: sxfake.KindTranscription, ClientReferenceID: "remora:old",
		Status: sxfake.StatusCompleted, FileID: "file-remora-old", CreatedAt: now.Add(-2 * time.Hour),
	})
	seed(sxfake.Object{
		ID: "file-remora-new", Kind: sxfake.KindFile, ClientReferenceID: "remora:new",
		CreatedAt: now.Add(-10 * time.Minute),
	})
	seed(sxfake.Object{
		ID: "file-other-old", Kind: sxfake.KindFile, ClientReferenceID: "other:old",
		CreatedAt: now.Add(-5 * time.Hour),
	})

	ids := func(objs []sxfake.Object) []string {
		out := make([]string, 0, len(objs))
		for _, o := range objs {
			out = append(out, o.ID)
		}
		slices.Sort(out)
		return out
	}
	st := waitSoniox(t, 10*time.Second, func(st sxfake.State) bool { return len(st.Deleted) >= 2 })
	if got, want := ids(st.Deleted), []string{"file-remora-old", "tx-remora-old"}; !slices.Equal(got, want) {
		t.Errorf("swept %q, want %q", got, want)
	}
	if got, want := ids(st.Live), []string{"file-other-old", "file-remora-new"}; !slices.Equal(got, want) {
		t.Errorf("left %q live, want %q", got, want)
	}
}
