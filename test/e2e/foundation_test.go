//go:build e2e

package e2e

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

type engineVersion struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type versionResponse struct {
	Remora  string          `json:"remora"`
	Engines []engineVersion `json:"engines"`
}

// get fetches url from the host and returns the status code and body.
func get(t *testing.T, url string) (int, []byte) {
	t.Helper()
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("GET %s: read body: %v", url, err)
	}
	return resp.StatusCode, body
}

// do sends a request with an optional JSON body and returns the status code and body.
func do(t *testing.T, method, url, body string) (int, []byte) {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, r)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("%s %s: read body: %v", method, url, err)
	}
	return resp.StatusCode, out
}

// engineVersions reads engines/versions.env, the one place that names each engine release.
func engineVersions(t *testing.T) map[string]string {
	t.Helper()
	f, err := os.Open("../../engines/versions.env")
	if err != nil {
		t.Fatalf("open versions.env: %v", err)
	}
	defer f.Close()
	vars := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		vars[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read versions.env: %v", err)
	}
	return vars
}

func wantEngines(t *testing.T) []engineVersion {
	t.Helper()
	v := engineVersions(t)
	if v["BIFROST_VERSION"] == "" || v["CLIPROXYAPI_VERSION"] == "" {
		t.Fatalf("versions.env is missing an engine: %v", v)
	}
	return []engineVersion{
		{Name: "bifrost", Version: v["BIFROST_VERSION"]},
		{Name: "cliproxyapi", Version: v["CLIPROXYAPI_VERSION"]},
	}
}

func fetchVersion(t *testing.T) versionResponse {
	t.Helper()
	code, body := get(t, remoraURL(t, "/api/system/version"))
	if code != http.StatusOK {
		t.Fatalf("GET /api/system/version: status %d, body %s", code, body)
	}
	var v versionResponse
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("decode version: %v: %s", err, body)
	}
	return v
}

func TestHealthz(t *testing.T) {
	code, body := get(t, remoraURL(t, "/healthz"))
	if code != http.StatusOK {
		t.Fatalf("status %d, want 200; body %s", code, body)
	}
	var h struct {
		Status string   `json:"status"`
		Down   []string `json:"down"`
	}
	if err := json.Unmarshal(body, &h); err != nil {
		t.Fatalf("decode: %v: %s", err, body)
	}
	if h.Status != "ok" || len(h.Down) != 0 {
		t.Fatalf("got %s, want {\"status\":\"ok\"}", body)
	}
}

func TestSystemVersionListsEngines(t *testing.T) {
	want := wantEngines(t)
	v := fetchVersion(t)
	if v.Remora == "" {
		t.Error("remora version is empty")
	}
	if !reflect.DeepEqual(v.Engines, want) {
		t.Errorf("engines = %+v, want %+v", v.Engines, want)
	}
}

func TestEnginePortsNotReachableFromOutside(t *testing.T) {
	// The engine ports are published in compose.yaml on purpose. The engines
	// bind 127.0.0.1 inside the container, so the forwarded connection fails.
	client := &http.Client{Timeout: 3 * time.Second}
	for _, port := range []int{18080, 18317} {
		url := "http://" + hostAddr(t, "remora", port) + "/"
		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			t.Errorf("port %d reachable from the host: GET %s -> %d", port, url, resp.StatusCode)
		}
	}

	// From inside the compose network.
	if out, code := execIn(t, "fakes", "/fakes", "get", "http://remora:18080/health"); code != 1 {
		t.Errorf("front door reachable from the network: code %d, stdout %q", code, out)
	}
	// Control: the public port is reachable from the same place.
	if out, code := execIn(t, "fakes", "/fakes", "get", "http://remora:8080/healthz"); code != 0 {
		t.Errorf("remora not reachable from the network: code %d, stdout %q", code, out)
	}
}

func TestFrontDoorUINotServed(t *testing.T) {
	if out, code := execIn(t, "remora", "remora", "healthcheck", "http://127.0.0.1:18080/health"); code != 0 {
		t.Fatalf("front door /health from inside: code %d, stdout %q", code, out)
	}
	out, code := execIn(t, "remora", "remora", "healthcheck", "http://127.0.0.1:18080/")
	if strings.TrimSpace(out) != "404" || code != 1 {
		t.Errorf("front door /: stdout %q code %d, want \"404\" and 1", out, code)
	}
	_, body := get(t, remoraURL(t, "/"))
	if strings.Contains(strings.ToLower(string(body)), "bifrost") {
		t.Errorf("remora / names an engine: %s", body)
	}
}

func TestOAuthEnginePanelNotServed(t *testing.T) {
	// With management.disable-control-panel: true, CLIProxyAPI v8.0.28 is
	// expected to answer 404 for the panel page. TODO(e2e first run): confirm
	// against v8.0.28. If it answers something else, assert instead that the
	// body has no "<html", and record here which one held.
	out, code := execIn(t, "remora", "remora", "healthcheck", "http://127.0.0.1:18317/management.html")
	if strings.TrimSpace(out) != "404" || code != 1 {
		t.Errorf("OAuth engine panel: stdout %q code %d, want \"404\" and 1", out, code)
	}
}

func TestFakesControlSmoke(t *testing.T) {
	resetFakes(t)

	code, body := do(t, http.MethodPut, fakesURL(t, "/_control/upstreams/smoke/routes/openai.chat"),
		`{"steps":[{"status":429}]}`)
	if code != http.StatusNoContent {
		t.Fatalf("PUT script: status %d, body %s", code, body)
	}

	code, body = do(t, http.MethodPost, fakesURL(t, "/u/smoke/v1/chat/completions"),
		`{"model":"m","messages":[{"role":"user","content":"hi"}]}`)
	if code != http.StatusTooManyRequests {
		t.Fatalf("scripted call: status %d, want 429; body %s", code, body)
	}

	code, body = get(t, fakesURL(t, "/_control/upstreams/smoke/requests"))
	if code != http.StatusOK {
		t.Fatalf("GET requests: status %d, body %s", code, body)
	}
	var recs []json.RawMessage
	if err := json.Unmarshal(body, &recs); err != nil {
		t.Fatalf("decode requests: %v: %s", err, body)
	}
	if len(recs) != 1 {
		t.Fatalf("recorded %d requests, want 1: %s", len(recs), body)
	}
}

// TestRestartKeepsState restarts remora on the same /data volume. It runs last
// in this file because it restarts the container.
func TestRestartKeepsState(t *testing.T) {
	if _, err := compose(t, "restart", "remora"); err != nil {
		t.Fatalf("restart: %v", err)
	}
	waitHealthy(t, 180*time.Second)

	want := wantEngines(t)
	v := fetchVersion(t)
	if !reflect.DeepEqual(v.Engines, want) {
		t.Errorf("after restart engines = %+v, want %+v", v.Engines, want)
	}

	logs, err := compose(t, "logs", "--no-color", "remora")
	if err != nil {
		t.Fatalf("logs: %v", err)
	}
	if strings.Contains(logs, "admin password generated") {
		t.Error("remora logged a generated admin password although one was provided")
	}
}
