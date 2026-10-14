package main

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/47vigen/remora/test/fakes/control"
	"github.com/47vigen/remora/test/fakes/soniox"
)

func do(t *testing.T, method, url, contentType string, body io.Reader) (int, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, url, body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer k")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, b
}

func TestHandlerMountsSonioxFake(t *testing.T) {
	srv := httptest.NewServer(newHandler())
	t.Cleanup(srv.Close)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", "a.wav")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write([]byte("audio"))
	_ = mw.Close()
	if code, b := do(t, http.MethodPost, srv.URL+"/u/soniox/v1/files", mw.FormDataContentType(), &buf); code != http.StatusCreated {
		t.Fatalf("upload = %d %s", code, b)
	}
	code, b := do(t, http.MethodGet, srv.URL+"/u/soniox/v1/files", "", nil)
	if code != http.StatusOK || !strings.Contains(string(b), `"filename":"a.wav"`) {
		t.Fatalf("list = %d %s", code, b)
	}

	// A scripted step on a Soniox route still wins over the fake.
	script, _ := json.Marshal(control.Script{Steps: []control.Step{{Status: http.StatusTooManyRequests, Body: `{"message":"slow down"}`}}})
	if code, b := do(t, http.MethodPut, srv.URL+"/_control/upstreams/soniox/routes/"+control.RouteSonioxTxCreate, "application/json", bytes.NewReader(script)); code != http.StatusNoContent {
		t.Fatalf("script = %d %s", code, b)
	}
	if code, _ := do(t, http.MethodPost, srv.URL+"/u/soniox/v1/transcriptions", "application/json", strings.NewReader(`{}`)); code != http.StatusTooManyRequests {
		t.Fatalf("scripted create = %d", code)
	}

	code, b = do(t, http.MethodGet, srv.URL+"/_control/soniox/state", "", nil)
	var st soniox.State
	if code != http.StatusOK || json.Unmarshal(b, &st) != nil || len(st.Live) != 1 {
		t.Fatalf("state = %d %s", code, b)
	}

	if code, _ := do(t, http.MethodPost, srv.URL+"/_control/reset", "", nil); code != http.StatusNoContent {
		t.Fatalf("reset = %d", code)
	}
	_, b = do(t, http.MethodGet, srv.URL+"/_control/soniox/state", "", nil)
	if err := json.Unmarshal(b, &st); err != nil || len(st.Live) != 0 {
		t.Fatalf("state after reset = %s", b)
	}
	if code, b := do(t, http.MethodGet, srv.URL+"/_control/upstreams/soniox/requests", "", nil); code != http.StatusOK || string(b) != "[]" {
		t.Fatalf("requests after reset = %d %s", code, b)
	}
}
