//go:build e2e

package e2e

import (
	"net/http"
	"testing"
	"time"

	"github.com/47vigen/remora/test/fakes/control"
)

// coolChat sends a chat completion for the cool alias with the cool key.
func coolChat(t *testing.T) reply {
	t.Helper()
	return doV1(t, http.MethodPost, "/v1/chat/completions", keyCool, chatBody("cool", false))
}

// served reports whether rep is a 200 served by servedBy with x-remora-fallback set to fallback.
func served(rep reply, servedBy string, fallback bool) bool {
	want := "false"
	if fallback {
		want = "true"
	}
	return rep.Status == http.StatusOK &&
		rep.Header.Get("X-Remora-Served-By") == servedBy && rep.Header.Get("X-Remora-Fallback") == want
}

// awaitServed sends send every interval until an answer is a 200 served by servedBy without a fallback, and fails
// the test when none is by timeout. Cooling is applied by a poller, so a test sees its effect only eventually.
func awaitServed(t *testing.T, what string, send func() reply, servedBy string, interval, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		rep := send()
		if served(rep, servedBy, false) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: not served by %s without fallback within %s; last status %d, served-by %q, fallback %q, body %s",
				what, servedBy, timeout, rep.Status, rep.Header.Get("X-Remora-Served-By"),
				rep.Header.Get("X-Remora-Fallback"), rep.Body)
		}
		time.Sleep(interval)
	}
}

// TestCoolingAndRecovery: a 402 with a retry hint cools its alias member, so later requests skip it without a
// fallback and never reach it; once the hint runs out and the upstream is healthy again, the member serves again.
func TestCoolingAndRecovery(t *testing.T) {
	resetFakes(t)
	script(t, "cool-a", control.RouteOpenAIChat, control.Script{
		Steps:  []control.Step{{Status: http.StatusPaymentRequired, Headers: map[string]string{"Retry-After": "4"}, Body: `{"error":{"message":"out of credit"}}`}},
		Repeat: true,
	})

	rep := coolChat(t)
	wantStatus(t, rep, http.StatusOK)
	wantHeader(t, rep, "X-Remora-Served-By", "cool-b/m")
	wantHeader(t, rep, "X-Remora-Fallback", "true")

	// Eventually cool-a is out of the chain: cool-b answers first-hand, and cool-a sees no more traffic. If the
	// 4s cooldown ends in the middle of the check, cool-a is tried, answers 402 and cools again, so check again.
	deadline := time.Now().Add(10 * time.Second)
	for !served(coolChat(t), "cool-b/m", false) || !quietAcross(t, 3) {
		if time.Now().After(deadline) {
			t.Fatalf("cool-a did not leave the chain within 10s")
		}
		time.Sleep(500 * time.Millisecond)
	}

	// The upstream recovers: back to the route's default 200. Once the cooldown ends, cool-a serves first again.
	script(t, "cool-a", control.RouteOpenAIChat, control.Script{Steps: []control.Step{}})
	awaitServed(t, "cool after recovery", func() reply { return coolChat(t) }, "cool-a/m", 500*time.Millisecond, 15*time.Second)
}

// quietAcross sends n requests to the cool alias and reports whether every one was served by cool-b without a
// fallback while cool-a received nothing.
func quietAcross(t *testing.T, n int) bool {
	t.Helper()
	before := len(received(t, "cool-a", control.RouteOpenAIChat))
	for range n {
		rep := coolChat(t)
		wantStatus(t, rep, http.StatusOK)
		if !served(rep, "cool-b/m", false) {
			return false
		}
	}
	return len(received(t, "cool-a", control.RouteOpenAIChat)) == before
}

// TestNoDroppedParamsHeaderForNonCodex: sampling parameters sent to a member that is not a Codex account reach it
// as they are, so no x-remora-dropped-params header is set.
func TestNoDroppedParamsHeaderForNonCodex(t *testing.T) {
	resetFakes(t)
	body := chatBody("fast", false)
	body["temperature"] = 0.2
	body["top_p"] = 0.9
	body["max_tokens"] = 64

	rep := chat(t, body)
	wantStatus(t, rep, http.StatusOK)
	wantHeader(t, rep, "X-Remora-Served-By", "up-a/m1")
	if v, ok := rep.Header["X-Remora-Dropped-Params"]; ok {
		t.Errorf("x-remora-dropped-params = %q, want no header", v)
	}
}
