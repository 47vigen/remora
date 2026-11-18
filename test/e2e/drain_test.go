//go:build e2e

package e2e

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/47vigen/remora/test/fakes/control"
)

func TestDrainThroughNginx(t *testing.T) {
	const drainSeconds = 5 // Default for the Compose fixture.
	id, err := compose(t, "ps", "-q", "remora")
	if err != nil || strings.TrimSpace(id) == "" {
		t.Fatalf("find remora container: %q, %v", id, err)
	}
	id = strings.TrimSpace(id)
	resetFakes(t)
	defer resetFakes(t)
	entries := []string{
		sseChunk(t, "chatcmpl-drain", []map[string]any{{"index": 0, "delta": map[string]any{"role": "assistant", "content": "first"}}}, nil),
		sseChunk(t, "chatcmpl-drain", []map[string]any{{"index": 0, "delta": map[string]any{"content": "last"}}}, nil),
		sseChunk(t, "chatcmpl-drain", []map[string]any{{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}}, nil),
	}
	once(t, "a", control.RouteOpenAIChat, control.Step{SSE: entries, IntervalMS: 1500})

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	probeURL := remoraURL(t, "/v1/models")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, remoraURL(t, "/v1/chat/completions"),
		strings.NewReader(mustJSON(t, chatBody("fast", true))))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+keyFull)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stream status = %d, want 200", resp.StatusCode)
	}
	body := bufio.NewReader(resp.Body)
	first, err := body.ReadString('\n')
	if err != nil || !strings.HasPrefix(first, "data: ") {
		t.Fatalf("first SSE event: %q, %v", first, err)
	}

	// Wait for Docker even if an assertion fails, then restore the shared fixture for the rest of the suite.
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(stopCancel)
	//nolint:gosec // G204: the container ID comes from the test's compose project.
	stop := exec.CommandContext(stopCtx, "docker", "stop", "-t", strconv.Itoa(drainSeconds+5), id)
	var stderr bytes.Buffer
	stop.Stderr = &stderr
	if err := stop.Start(); err != nil {
		t.Fatalf("start docker stop: %v", err)
	}
	stopped := make(chan struct{})
	var stopErr error
	go func() {
		stopErr = stop.Wait()
		close(stopped)
	}()
	t.Cleanup(func() {
		<-stopped
		if stopErr != nil {
			t.Errorf("docker stop: %v\n%s", stopErr, stderr.String())
		}
		if out, err := compose(t, "start", "--wait", "--wait-timeout", "60", "remora"); err != nil {
			t.Fatalf("restore remora: %v\n%s", err, out)
		}
	})

	probe := &http.Client{Timeout: time.Second}
	probeReq, err := http.NewRequestWithContext(ctx, http.MethodGet, probeURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	probeReq.Header.Set("Authorization", "Bearer "+keyFull)
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	sawDrain := false
probing:
	for {
		res, err := probe.Do(probeReq.Clone(ctx))
		if err == nil {
			_ = res.Body.Close()
			switch {
			case res.StatusCode == http.StatusOK && !sawDrain:
				// The stop signal has not reached remora yet.
			case res.StatusCode == http.StatusServiceUnavailable && res.Header.Get("Retry-After") == "1" &&
				strings.HasPrefix(res.Header.Get("Content-Type"), "application/json"):
				sawDrain = true
			default:
				t.Fatalf("new /v1 request during stop: status %d, Retry-After %q, Content-Type %q; want 200 before drain or a JSON 503 with Retry-After: 1",
					res.StatusCode, res.Header.Get("Retry-After"), res.Header.Get("Content-Type"))
			}
		} else {
			disconnected := errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, io.EOF)
			// The host's Docker port forwarder may leave a socket hanging after the container exits.
			if errors.Is(err, context.DeadlineExceeded) {
				select {
				case <-stopped:
					disconnected = true
				default:
				}
			}
			if !sawDrain || !disconnected {
				t.Fatalf("new /v1 request during stop: %v", err)
			}
		}
		select {
		case <-stopped:
			break probing
		case <-tick.C:
		}
	}
	if !sawDrain {
		t.Fatal("no probe observed a drain response")
	}

	rest, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("stream was cut during drain: %v", err)
	}
	chunks := streamChunks(t, reply{Status: resp.StatusCode, Header: resp.Header, Body: append([]byte(first), rest...)})
	if len(chunks) == 0 {
		t.Fatal("no chunks in the drained stream")
	}
	if last := chunks[len(chunks)-1]; len(last.Choices) == 0 || last.Choices[0].FinishReason == nil || *last.Choices[0].FinishReason != "stop" {
		t.Fatal("the drained stream did not deliver its final chunk")
	}
}
