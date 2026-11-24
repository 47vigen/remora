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
				if f.status == http.StatusPaymentRequired {
					// Deferred, not a Cleanup: the requests need the test's context, which ends before cleanups run.
					defer awaitCoolingCycle(t)
				}
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

// awaitCoolingCycle waits out the cooldown a 402 from up-a starts: first until fast is served by up-b without a
// fallback (up-a is out of the chain), then until up-a serves it again, so later tests see the full chain.
func awaitCoolingCycle(t *testing.T) {
	t.Helper()
	send := func() reply { return chat(t, chatBody("fast", false)) }
	awaitServed(t, "fast while up-a cools", send, "up-b/m1", 200*time.Millisecond, 10*time.Second)
	awaitServed(t, "fast after up-a cooled", send, "up-a/m1", 500*time.Millisecond, 30*time.Second)
}

func TestAliasSkipsSubscriptionWithoutAccount(t *testing.T) {
	resetFakes(t)
	a := loginClient(t)
	id := importQuotaAccount(t, a, "antigravity", "antigravity")
	const name = "subscription-fallback"
	a.must(http.MethodPost, "/api/aliases", mustJSON(t, map[string]any{
		"name": name, "kind": "chat", "members": []string{"antigravity/m", "up-a/m1"},
	}), http.StatusCreated)
	t.Cleanup(func() { a.deleteIfAny("/api/aliases/" + name) })
	send := func() reply { return chat(t, chatBody(name, false)) }
	// The fixture signs in but does not serve m. Prove the alias initially tries that member before disabling it.
	eventually(t, 10*time.Second, "alias tries the enabled subscription account", func() bool {
		rep := send()
		// A structured model-access error can also fall back; either result proves the primary was tried.
		return served(rep, "up-a/m1", true) ||
			rep.Status == http.StatusBadRequest && strings.Contains(string(rep.Body), "unknown provider for model")
	})
	a.must(http.MethodPatch, "/api/accounts/"+id, `{"enabled":false}`, http.StatusOK)

	awaitServed(t, "alias with its subscription account disabled", send, "up-a/m1", 200*time.Millisecond, 10*time.Second)
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

// TestAliasNamedLikeProviderModel: "openai" is one of the front door's own providers, yet an alias of that shape
// reaches its member instead of being read as a direct provider model.
func TestAliasNamedLikeProviderModel(t *testing.T) {
	resetFakes(t)
	rep := chat(t, chatBody("openai/fast", false))
	wantStatus(t, rep, http.StatusOK)
	wantHeader(t, rep, "X-Remora-Alias", "openai/fast")
	wantHeader(t, rep, "X-Remora-Served-By", "up-b/m1")
	if n := len(received(t, "b", control.RouteOpenAIChat)); n != 1 {
		t.Errorf("up-b got %d chat requests, want 1", n)
	}
}

// TestReconcileAfterEngineRestart edits the front door directly and checks that a supervised restart repairs it
// without another apply.
func TestReconcileAfterEngineRestart(t *testing.T) {
	resetFakes(t)
	// Use the engine's loopback management API without exposing its port or returning its credentials to the host.
	const editFrontDoor = `
set -eo pipefail
request() {
    local method=$1 path=$2 body=$3 auth=$4
    exec 3<>/dev/tcp/127.0.0.1/18080
    printf '%s %s HTTP/1.1\r\nHost: 127.0.0.1:18080\r\nConnection: close\r\nContent-Type: application/json\r\nContent-Length: %s\r\n%s\r\n%s' \
        "$method" "$path" "${#body}" "$auth" "$body" >&3
    cat <&3
    exec 3>&-
}
password=$(sed -n 's/.*"admin_password": "\([A-Za-z0-9_-]*\)".*/\1/p' "$REMORA_DATA/bifrost/config.json")
[ -n "$password" ]
login=$(printf '{"username":"remora","password":"%s"}' "$password")
session=$(request POST /api/session/login "$login" '' | sed -n 's/^Set-Cookie: token=\([^;]*\);.*/\1/ip')
[ -n "$session" ]
request PUT /api/providers/up-a "$1" "Authorization: Bearer $session"$'\r\n' | awk 'NR == 1 && $2 ~ /^[0-9][0-9][0-9]$/ {print $2}'
`
	const driftedProvider = `{"network_config":{"base_url":"http://fakes:9090/u/c","allow_private_network":true,` +
		`"default_request_timeout_in_seconds":600,"stream_idle_timeout_in_seconds":300},` +
		`"concurrency_and_buffer_size":{"concurrency":1000,"buffer_size":5000},` +
		`"custom_provider_config":{"base_provider_type":"openai"}}`
	out, code := execIn(t, "remora", "timeout", "30s", "bash", "-c", editFrontDoor, "edit-provider", driftedProvider)
	if code != 0 || strings.TrimSpace(out) != "200" {
		t.Fatalf("edit provider directly: exit %d, HTTP status %q", code, strings.TrimSpace(out))
	}
	rep := chat(t, chatBody("fast", false))
	wantStatus(t, rep, http.StatusOK)
	wantHeader(t, rep, "X-Remora-Served-By", "up-a/m1")
	if n := len(received(t, "c", control.RouteOpenAIChat)); n != 1 {
		t.Fatalf("direct engine edit sent %d requests to up-c, want 1", n)
	}
	resetFakes(t)

	const killFrontDoor = `n=0; for d in /proc/[0-9]*; do ` +
		`if [ "$(readlink "$d/exe" 2>/dev/null)" = /usr/local/lib/remora/engines/bifrost ]; then ` +
		`kill "${d#/proc/}" && n=$((n+1)); fi; done; echo "$n"`
	out, code = execIn(t, "remora", "sh", "-c", killFrontDoor)
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

	// The full pass runs on the normal reconcile timer (60 s), not on the health transition.
	deadline = time.Now().Add(90 * time.Second)
	for {
		rep := chat(t, chatBody("fast", false))
		if served(rep, "up-a/m1", false) && len(received(t, "a", control.RouteOpenAIChat)) > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("fast did not reach up-a's original upstream after restart: last status %d", rep.Status)
		}
		time.Sleep(time.Second)
	}
}

// TestNoFallbackOnCallerFault: a primary that refuses the request itself answers the client; no other member is
// tried with a request already known to be malformed.
func TestNoFallbackOnCallerFault(t *testing.T) {
	const body = `{"error":{"message":"bad request","type":"invalid_request_error","param":"messages","code":"invalid_value"}}`
	for _, status := range []int{http.StatusBadRequest, http.StatusRequestEntityTooLarge, http.StatusUnprocessableEntity} {
		for _, stream := range []bool{false, true} {
			t.Run(strconv.Itoa(status)+"/stream="+strconv.FormatBool(stream), func(t *testing.T) {
				resetFakes(t)
				once(t, "a", control.RouteOpenAIChat, control.Step{Status: status, Body: body})
				rep := chat(t, chatBody("fast", stream))
				if rep.Status != status {
					t.Fatalf("status %d, want %d; body %s", rep.Status, status, rep.Body)
				}
				if f := rep.Header.Get("X-Remora-Fallback"); f != "" && f != "false" {
					t.Errorf("X-Remora-Fallback = %q", f)
				}
				if n := len(received(t, "a", control.RouteOpenAIChat)); n != 1 {
					t.Errorf("up-a got %d chat requests, want 1", n)
				}
				if n := len(received(t, "b", control.RouteOpenAIChat)); n != 0 {
					t.Errorf("up-b got %d chat requests, want 0", n)
				}
			})
		}
	}
}
