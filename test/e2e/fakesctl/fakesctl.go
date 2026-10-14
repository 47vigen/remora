// Package fakesctl is the test-side client of the fakes' control API: it
// scripts a fake upstream's replies and reads back what the upstream received.
// It carries no build tag so any test package can import it.
package fakesctl

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/47vigen/remora/test/fakes/control"
)

// Script sets the script of one (upstream, route) through
// PUT /_control/upstreams/{upstream}/routes/{route}. base is the fakes' root
// URL, such as http://127.0.0.1:9090.
func Script(t testing.TB, base, upstream, route string, s control.Script) {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("fakesctl: marshal script: %v", err)
	}
	call(t, http.MethodPut, base+"/_control/upstreams/"+upstream+"/routes/"+route, b, http.StatusNoContent)
}

// Requests returns GET /_control/upstreams/{upstream}/requests: every request
// the upstream received since the last reset, in arrival order.
func Requests(t testing.TB, base, upstream string) []control.RecordedRequest {
	t.Helper()
	body := call(t, http.MethodGet, base+"/_control/upstreams/"+upstream+"/requests", nil, http.StatusOK)
	var out []control.RecordedRequest
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("fakesctl: decode requests: %v (body %q)", err, body)
	}
	return out
}

// Reset clears every script and recording through POST /_control/reset.
func Reset(t testing.TB, base string) {
	t.Helper()
	call(t, http.MethodPost, base+"/_control/reset", nil, http.StatusNoContent)
}

func call(t testing.TB, method, url string, body []byte, want int) []byte {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("fakesctl: %s %s: %v", method, url, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	//nolint:gosec // G704: url is the test's own fakes address.
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("fakesctl: %s %s: %v", method, url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("fakesctl: %s %s: read body: %v", method, url, err)
	}
	if resp.StatusCode != want {
		t.Fatalf("fakesctl: %s %s: status %d, want %d (body %q)", method, url, resp.StatusCode, want, b)
	}
	return b
}
