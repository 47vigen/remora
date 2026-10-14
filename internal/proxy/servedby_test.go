package proxy

import (
	"net/http"
	"testing"

	"github.com/47vigen/remora/internal/engine/cliproxyapi"
)

func TestServedByHidesInternalProvider(t *testing.T) {
	cases := []struct {
		name, provider, model, body string
		stream                      bool
		wantServedBy, wantDropped   string
	}{
		{
			name: "oauth codex", provider: "remora-oauth", model: "codex/gpt-x",
			body: `{"model":"codex/gpt-x","messages":[]}`, wantServedBy: "codex/gpt-x",
		},
		{
			// The dropped-params step runs after the hooks, so it sees the renamed Codex member.
			name: "oauth codex, dropped params", provider: "remora-oauth", model: "codex/gpt-x",
			body: `{"model":"codex/gpt-x","messages":[],"temperature":0.5}`, wantServedBy: "codex/gpt-x",
			wantDropped: "temperature",
		},
		{
			name: "oauth codex stream, dropped params", provider: "remora-oauth", model: "codex/gpt-x", stream: true,
			body: `{"model":"codex/gpt-x","messages":[],"stream":true,"top_p":1}`, wantServedBy: "codex/gpt-x",
			wantDropped: "top_p",
		},
		{
			name: "oauth grok", provider: "remora-oauth", model: "grok/g-1",
			body: `{"model":"grok/g-1","messages":[],"temperature":0.5}`, wantServedBy: "grok/g-1",
		},
		{
			name: "api-key provider", provider: "openrouter", model: "a/b",
			body: `{"model":"openrouter/a/b","messages":[],"temperature":0.5}`, wantServedBy: "openrouter/a/b",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			f.px.OnResponse(ServedByHook(cliproxyapi.UserRef))
			f.upstream(servedBy(c.provider, c.model, c.stream))
			resp := f.do(t, http.MethodPost, "/v1/chat/completions", keyFull, "application/json", []byte(c.body), nil)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status %d: %s", resp.StatusCode, resp.Body)
			}
			if got := resp.Header.Get("x-remora-served-by"); got != c.wantServedBy {
				t.Errorf("x-remora-served-by = %q, want %q", got, c.wantServedBy)
			}
			if got := resp.Header.Get("x-remora-dropped-params"); got != c.wantDropped {
				t.Errorf("x-remora-dropped-params = %q, want %q", got, c.wantDropped)
			}
		})
	}
}
