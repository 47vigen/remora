//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/47vigen/remora/test/fakes/control"
)

func chatBody(model string, stream bool) map[string]any {
	b := map[string]any{
		"model":    model,
		"messages": []map[string]any{{"role": "user", "content": persian}},
	}
	if stream {
		b["stream"] = true
	}
	return b
}

func TestFallbackWithinRequest(t *testing.T) {
	failures := []struct {
		name   string
		status int
		body   string
	}{
		{"rate-limit", http.StatusTooManyRequests, `{"error":{"type":"rate_limit_exceeded"}}`},
		{"quota", http.StatusTooManyRequests, `{"error":{"code":"insufficient_quota"}}`},
		{"credit", http.StatusPaymentRequired, `{"error":{"message":"out of credit"}}`},
	}
	for _, f := range failures {
		for _, stream := range []bool{false, true} {
			t.Run(f.name+"/stream="+strconv.FormatBool(stream), func(t *testing.T) {
				resetFakes(t)
				once(t, "a", control.RouteOpenAIChat, control.Step{Status: f.status, Body: f.body})

				rep := chat(t, chatBody("fast", stream))
				wantStatus(t, rep, http.StatusOK)
				if stream {
					streamChunks(t, rep)
				}
				wantHeader(t, rep, "X-Remora-Alias", "fast")
				wantHeader(t, rep, "X-Remora-Served-By", "up-b/m1")
				wantHeader(t, rep, "X-Remora-Fallback", "true")
				if n := len(received(t, "a", control.RouteOpenAIChat)); n != 1 {
					t.Errorf("up-a got %d chat requests, want 1", n)
				}
				if n := len(received(t, "b", control.RouteOpenAIChat)); n != 1 {
					t.Errorf("up-b got %d chat requests, want 1", n)
				}
			})
		}
	}
}

func TestFallbackAllFail(t *testing.T) {
	resetFakes(t)
	fail := control.Script{Steps: []control.Step{{Status: http.StatusTooManyRequests, Body: `{"error":{"type":"rate_limit_exceeded"}}`}}, Repeat: true}
	script(t, "a", control.RouteOpenAIChat, fail)
	script(t, "b", control.RouteOpenAIChat, fail)

	rep := chat(t, chatBody("fast", false))
	wantStatus(t, rep, http.StatusTooManyRequests)
	var body struct {
		Error map[string]json.RawMessage `json:"error"`
	}
	decodeJSON(t, rep.Body, &body)
	if len(body.Error) == 0 {
		t.Errorf("body %s, want an OpenAI {\"error\":{...}} object", rep.Body)
	}
	if _, ok := body.Error["message"]; !ok {
		t.Errorf("error has no message: %s", rep.Body)
	}
}

func TestNestedAlias(t *testing.T) {
	resetFakes(t)
	once(t, "a", control.RouteOpenAIChat, control.Step{Status: http.StatusTooManyRequests, Body: `{"error":{"type":"rate_limit_exceeded"}}`})

	rep := chat(t, chatBody("memory/fast", false))
	wantStatus(t, rep, http.StatusOK)
	wantHeader(t, rep, "X-Remora-Alias", "memory/fast")
	wantHeader(t, rep, "X-Remora-Served-By", "up-c/m1")
	if n := len(received(t, "a", control.RouteOpenAIChat)); n != 1 {
		t.Errorf("up-a got %d chat requests, want 1", n)
	}
	// inner holds only up-a, so up-b is never tried.
	if n := len(received(t, "b", control.RouteOpenAIChat)); n != 0 {
		t.Errorf("up-b got %d chat requests, want 0", n)
	}
}

// TestReconcileAfterEngineRestart kills the front door inside the container; the supervisor restarts it, and the
// aliases and keys work again without another apply.
func TestReconcileAfterEngineRestart(t *testing.T) {
	resetFakes(t)
	const killFrontDoor = `n=0; for d in /proc/[0-9]*; do ` +
		`if [ "$(readlink "$d/exe" 2>/dev/null)" = /usr/local/lib/remora/engines/bifrost ]; then ` +
		`kill "${d#/proc/}" && n=$((n+1)); fi; done; echo "$n"`
	out, code := execIn(t, "remora", "sh", "-c", killFrontDoor)
	if code != 0 || strings.TrimSpace(out) != "1" {
		t.Fatalf("kill front door: exit %d, killed %q processes, want 1", code, strings.TrimSpace(out))
	}

	// Give the supervisor a moment to see the exit, so health below is the restarted engine's.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if c, _ := get(t, remoraURL(t, "/healthz")); c != http.StatusOK {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	waitHealthy(t, 120*time.Second)

	deadline = time.Now().Add(30 * time.Second)
	for {
		rep := chat(t, chatBody("fast", false))
		if rep.Status == http.StatusOK {
			wantHeader(t, rep, "X-Remora-Served-By", "up-a/m1")
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("fast after restart: status %d, body %s", rep.Status, rep.Body)
		}
		time.Sleep(time.Second)
	}
}
