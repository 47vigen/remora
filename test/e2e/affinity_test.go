//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/47vigen/remora/test/fakes/control"
)

// affinityOf sends a chat to fast with the extra request headers and returns the X-Session-Affinity header up-a
// received.
func affinityOf(t *testing.T, header http.Header, messages []map[string]string) string {
	t.Helper()
	resetFakes(t)
	body, err := json.Marshal(map[string]any{"model": "fast", "messages": messages})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, remoraURL(t, "/v1/chat/completions"), bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header = header.Clone()
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+keyFull)
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	out, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	wantStatus(t, reply{Status: resp.StatusCode, Header: resp.Header, Body: out}, http.StatusOK)
	got := received(t, "a", control.RouteOpenAIChat)
	if len(got) != 1 {
		t.Fatalf("up-a got %d chat requests, want 1", len(got))
	}
	for name := range got[0].Header {
		if strings.HasPrefix(strings.ToLower(name), "x-bf-") {
			t.Errorf("upstream got front-door control header %s", name)
		}
	}
	return got[0].Header.Get("X-Session-Affinity")
}

func TestAffinityHeaderForwarded(t *testing.T) {
	h := http.Header{}
	h.Set("X-Session-ID", "e2e-1")
	if got := affinityOf(t, h, []map[string]string{{"role": "user", "content": "hi"}}); got != "e2e-1" {
		t.Fatalf("X-Session-Affinity = %q, want e2e-1", got)
	}
}

func TestAffinityDerivedPerConversation(t *testing.T) {
	format := regexp.MustCompile(`^aff_[0-9a-f]{32}$`)
	sys := map[string]string{"role": "system", "content": "a shared system prompt"}
	a := affinityOf(t, http.Header{}, []map[string]string{sys, {"role": "user", "content": persian}})
	b := affinityOf(t, http.Header{}, []map[string]string{sys, {"role": "user", "content": persian + "2"}})
	a2 := affinityOf(t, http.Header{}, []map[string]string{
		sys, {"role": "user", "content": persian}, {"role": "assistant", "content": "ok"}, {"role": "user", "content": "next"},
	})
	if !format.MatchString(a) || !format.MatchString(b) || a == b || a != a2 {
		t.Fatalf("a=%q b=%q a2=%q: turns of one conversation share a key, another conversation does not", a, b, a2)
	}
}
