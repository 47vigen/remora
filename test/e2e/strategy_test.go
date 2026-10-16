//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/47vigen/remora/test/fakes/control"
)

// Each test uses its own models, so the 60 s start counts of one test never touch another's.

// chatFirst sends a chat completion that carries the client's own X-Remora-First under the header name name.
func chatFirst(t *testing.T, model, name, value string) reply {
	t.Helper()
	b, err := json.Marshal(chatBody(model, false))
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, remoraURL(t, "/v1/chat/completions"), bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+keyFull)
	req.Header[name] = []string{value}
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	rep := reply{Status: resp.StatusCode, Header: resp.Header, Body: out}
	checkLeaks(t, "POST /v1/chat/completions", rep)
	return rep
}

func TestFirstHeaderNeverReachesUpstream(t *testing.T) {
	resetFakes(t)
	var served []string
	for _, name := range []string{"X-Remora-First", "x-remora-first", "X-REMORA-FIRST"} {
		rep := chatFirst(t, "spread-rr", name, "up-c/rr")
		wantStatus(t, rep, http.StatusOK)
		served = append(served, rep.Header.Get("X-Remora-Served-By"))
	}
	slices.Sort(served)
	if want := []string{"up-a/rr", "up-b/rr", "up-c/rr"}; !slices.Equal(served, want) {
		t.Errorf("served by %v, want %v: the client's header must not pin a member", served, want)
	}
	for _, up := range []string{"a", "b", "c"} {
		for _, r := range received(t, up, control.RouteOpenAIChat) {
			for h := range r.Header {
				if strings.EqualFold(h, "X-Remora-First") {
					t.Errorf("up-%s received %s: %q", up, h, r.Header[h])
				}
			}
		}
	}
}

func TestRoundRobinSpreads(t *testing.T) {
	resetFakes(t)
	for range 6 {
		wantStatus(t, chat(t, chatBody("spread-rr", false)), http.StatusOK)
	}
	for _, up := range []string{"a", "b", "c"} {
		if n := len(received(t, up, control.RouteOpenAIChat)); n != 2 {
			t.Errorf("up-%s got %d requests, want 2", up, n)
		}
	}
}

func TestLeastUsedSpreads(t *testing.T) {
	resetFakes(t)
	for _, want := range []string{"up-a/lu", "up-b/lu", "up-c/lu"} {
		rep := chat(t, chatBody("spread-lu", false))
		wantStatus(t, rep, http.StatusOK)
		wantHeader(t, rep, "X-Remora-Served-By", want)
	}
}

func TestFallbackAfterPick(t *testing.T) {
	resetFakes(t)
	// compose sets REMORA_COOLING_429_COUNT high, so one 429 without Retry-After never cools up-a.
	once(t, "a", control.RouteOpenAIChat, control.Step{Status: http.StatusTooManyRequests, Body: `{"error":{"type":"rate_limit_exceeded"}}`})
	rep := chat(t, chatBody("spread-fb", false))
	wantStatus(t, rep, http.StatusOK)
	wantHeader(t, rep, "X-Remora-Served-By", "up-b/fb")
	wantHeader(t, rep, "X-Remora-Fallback", "true")
	// The start counted where it landed, so up-a is now the least used.
	rep = chat(t, chatBody("spread-fb", false))
	wantStatus(t, rep, http.StatusOK)
	wantHeader(t, rep, "X-Remora-Served-By", "up-a/fb")
}
