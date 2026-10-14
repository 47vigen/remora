//go:build e2e

package e2e

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"regexp"
	"strings"
	"testing"
	"time"
)

// adminPassword is the test-only admin password compose.yaml sets; it is never a real secret.
const adminPassword = "e2e-admin-password" //nolint:gosec // G101: a fixed test value from compose.yaml.

// uiGet fetches a path from remora's public port with client and reads the whole answer.
func uiGet(t *testing.T, client *http.Client, path string) reply {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, remoraURL(t, path), nil)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("GET %s: read body: %v", path, err)
	}
	return reply{Status: resp.StatusCode, Header: resp.Header, Body: body}
}

// signedIn returns a client that holds an admin session cookie.
func signedIn(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	client := &http.Client{Timeout: 10 * time.Second, Jar: jar}
	body := strings.NewReader(`{"password":"` + adminPassword + `"}`)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, remoraURL(t, "/api/auth/login"), body)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("login: status %d, want 204", resp.StatusCode)
	}
	return client
}

// wantNotHTML fails when an API answer is the console document or any other HTML.
func wantNotHTML(t *testing.T, path string, rep reply) {
	t.Helper()
	body := string(rep.Body)
	if ct := rep.Header.Get("Content-Type"); strings.HasPrefix(ct, "text/html") {
		t.Errorf("GET %s: content-type %q, want not HTML", path, ct)
	}
	if strings.Contains(strings.ToLower(body), "<html") || strings.Contains(body, `<div id="root">`) {
		t.Errorf("GET %s: body is HTML: %.300s", path, body)
	}
}

// wantIndex checks that resp is the console's index document, always revalidated.
func wantIndex(t *testing.T, path string, rep reply) {
	t.Helper()
	body := string(rep.Body)
	if rep.Status != http.StatusOK {
		t.Fatalf("GET %s: status %d, want 200", path, rep.Status)
	}
	if ct := rep.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("GET %s: content-type %q, want text/html", path, ct)
	}
	if cc := rep.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("GET %s: cache-control %q, want no-cache", path, cc)
	}
	if !strings.Contains(body, `<div id="root">`) {
		t.Errorf("GET %s: body has no <div id=\"root\">: %.300s", path, body)
	}
}

// assetScript finds the first script under /assets/ that the index document loads.
var assetScript = regexp.MustCompile(`src="(/assets/[^"]+\.js)"`)

func TestUIServed(t *testing.T) {
	anon := &http.Client{Timeout: 10 * time.Second}
	root := uiGet(t, anon, "/")
	wantIndex(t, "/", root)

	// A deep link is the same document: the app routes on the client.
	logs := uiGet(t, anon, "/logs")
	wantIndex(t, "/logs", logs)
	if string(logs.Body) != string(root.Body) {
		t.Errorf("GET /logs is not the index document")
	}

	m := assetScript.FindSubmatch(root.Body)
	if m == nil {
		t.Fatalf("index loads no /assets/*.js: %.300s", root.Body)
	}
	asset := string(m[1])
	rep := uiGet(t, anon, asset)
	if rep.Status != http.StatusOK {
		t.Fatalf("GET %s: status %d, want 200", asset, rep.Status)
	}
	if cc := rep.Header.Get("Cache-Control"); cc != "public, max-age=31536000, immutable" {
		t.Errorf("GET %s: cache-control %q, want public, max-age=31536000, immutable", asset, cc)
	}

	// An unknown API path is an API answer, never the console: 401 without a session, 404 with one.
	rep = uiGet(t, anon, "/api/does-not-exist")
	if rep.Status != http.StatusUnauthorized {
		t.Errorf("GET /api/does-not-exist without a session: status %d, want 401", rep.Status)
	}
	wantNotHTML(t, "/api/does-not-exist", rep)
	rep = uiGet(t, signedIn(t), "/api/does-not-exist")
	if rep.Status != http.StatusNotFound {
		t.Errorf("GET /api/does-not-exist: status %d, want 404", rep.Status)
	}
	wantNotHTML(t, "/api/does-not-exist", rep)
}
