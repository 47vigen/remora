package fakesctl_test

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/47vigen/remora/test/e2e/fakesctl"
	"github.com/47vigen/remora/test/fakes/control"
)

func TestScriptRequestsReset(t *testing.T) {
	srv := httptest.NewServer(control.New(control.Defaults()).Handler())
	t.Cleanup(srv.Close)

	fakesctl.Script(t, srv.URL, "a", control.RouteOpenAIChat, control.Script{Steps: []control.Step{
		{Status: http.StatusPaymentRequired, Body: `{"error":{"message":"out of credit"}}`},
	}})
	body := "{\"model\":\"m1\",\"messages\":[{\"role\":\"user\",\"content\":\"نمی\u200cخواهم\"}]}"
	codes := make([]int, 0, 2)
	for range 2 {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+"/u/a/v1/chat/completions",
			strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		codes = append(codes, resp.StatusCode)
	}
	if codes[0] != http.StatusPaymentRequired || codes[1] != http.StatusOK {
		t.Fatalf("codes = %v, want [402 200]", codes)
	}

	recs := fakesctl.Requests(t, srv.URL, "a")
	if len(recs) != 2 || recs[0].Route != control.RouteOpenAIChat || !bytes.Equal(recs[1].Body, []byte(body)) {
		t.Fatalf("recordings = %+v", recs)
	}
	if got := fakesctl.Requests(t, srv.URL, "b"); len(got) != 0 {
		t.Fatalf("upstream b recorded %d", len(got))
	}

	fakesctl.Reset(t, srv.URL)
	if got := fakesctl.Requests(t, srv.URL, "a"); len(got) != 0 {
		t.Fatalf("after reset recorded %d", len(got))
	}
}
