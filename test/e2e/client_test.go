//go:build e2e

package e2e

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/47vigen/remora/test/e2e/fakesctl"
	"github.com/47vigen/remora/test/fakes/control"
)

// Client key values, as compose.yaml sets them for testdata/remora.yaml.
const (
	keyFull    = "rk-e2e-full-key"
	keyLimited = "rk-e2e-limited-key"
	keyOff     = "rk-e2e-off-key"
)

// persian holds two zero-width non-joiners (U+200C); it is compared byte for byte.
const persian = "می\u200cشود و نمی\u200cخواهم"

// leak matches anything that would name an engine or show one of its keys to a client.
var leak = regexp.MustCompile(`(?i)bifrost|cliproxy|sk-bf-`)

// reply is a fully read /v1 response.
type reply struct {
	Status int
	Header http.Header
	Body   []byte
}

// doV1 sends a request to remora's /v1 surface and reads the whole answer. key is sent as a bearer token unless
// empty; body is sent as JSON (a string or []byte verbatim, anything else marshalled). It is the leak guard: the
// test fails when any response header name or value, or any body byte sequence, names an engine or one of its keys.
// foundation_test.go's do talks to the fakes and the management API, which may name engines; doV1 never may.
func doV1(t *testing.T, method, path, key string, body any) reply {
	t.Helper()
	var r io.Reader
	if body != nil {
		var b []byte
		switch v := body.(type) {
		case string:
			b = []byte(v)
		case []byte:
			b = v
		default:
			var err error
			if b, err = json.Marshal(v); err != nil {
				t.Fatalf("%s %s: marshal body: %v", method, path, err)
			}
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(t.Context(), method, remoraURL(t, path), r)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("%s %s: read body: %v", method, path, err)
	}
	rep := reply{Status: resp.StatusCode, Header: resp.Header, Body: out}
	checkLeaks(t, method+" "+path, rep)
	return rep
}

// checkLeaks fails the test when a header or the body of rep names an engine or carries an engine key.
func checkLeaks(t *testing.T, what string, rep reply) {
	t.Helper()
	for name, vals := range rep.Header {
		if leak.MatchString(name) {
			t.Errorf("%s: response header name %q leaks an engine", what, name)
		}
		for _, v := range vals {
			if leak.MatchString(v) {
				t.Errorf("%s: response header %s: %q leaks an engine", what, name, v)
			}
		}
	}
	if loc := leak.FindIndex(rep.Body); loc != nil {
		from, to := max(loc[0]-80, 0), min(loc[1]+80, len(rep.Body))
		t.Errorf("%s: body leaks an engine near %q", what, rep.Body[from:to])
	}
}

// chat sends a chat completion with the full key.
func chat(t *testing.T, body any) reply {
	t.Helper()
	return doV1(t, http.MethodPost, "/v1/chat/completions", keyFull, body)
}

// wantStatus fails the test unless rep has status want.
func wantStatus(t *testing.T, rep reply, want int) {
	t.Helper()
	if rep.Status != want {
		t.Fatalf("status %d, want %d; body %s", rep.Status, want, rep.Body)
	}
}

// wantHeader checks one response header.
func wantHeader(t *testing.T, rep reply, name, want string) {
	t.Helper()
	if got := rep.Header.Get(name); got != want {
		t.Errorf("%s = %q, want %q", name, got, want)
	}
}

// script sets the script of one fake (upstream, route).
func script(t *testing.T, upstream, route string, s control.Script) {
	t.Helper()
	fakesctl.Script(t, fakesURL(t, ""), upstream, route, s)
}

// once scripts one reply for an (upstream, route); later calls get the route's default.
func once(t *testing.T, upstream, route string, steps ...control.Step) {
	t.Helper()
	script(t, upstream, route, control.Script{Steps: steps})
}

// received returns the requests a fake upstream got on one route since the last reset. Filtering by route keeps
// unrelated engine traffic, such as a model listing, out of the counts.
func received(t *testing.T, upstream, route string) []control.RecordedRequest {
	t.Helper()
	var out []control.RecordedRequest
	for _, r := range fakesctl.Requests(t, fakesURL(t, ""), upstream) {
		if r.Route == route {
			out = append(out, r)
		}
	}
	return out
}

// mustJSON marshals v or fails the test.
func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

// decodeJSON unmarshals b into v or fails the test.
func decodeJSON(t *testing.T, b []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("decode %q: %v", b, err)
	}
}

// jsonEqual reports whether a and b hold the same JSON value.
func jsonEqual(t *testing.T, a, b []byte) bool {
	t.Helper()
	var x, y any
	decodeJSON(t, a, &x)
	decodeJSON(t, b, &y)
	return reflect.DeepEqual(x, y)
}

// sseData returns the data payload of every SSE event in body, in order. Comment lines and other fields are
// skipped; a multi-line data field is joined with "\n".
func sseData(t *testing.T, body []byte) []string {
	t.Helper()
	var (
		out  []string
		cur  []string
		have bool
	)
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			if have {
				out = append(out, strings.Join(cur, "\n"))
			}
			cur, have = nil, false
			continue
		}
		if v, ok := strings.CutPrefix(line, "data:"); ok {
			cur = append(cur, strings.TrimPrefix(v, " "))
			have = true
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read SSE: %v", err)
	}
	if have {
		out = append(out, strings.Join(cur, "\n"))
	}
	return out
}
