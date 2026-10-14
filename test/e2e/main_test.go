//go:build e2e

// Package e2e runs the built remora image and the fakes image with Docker
// Compose and tests them from the host. Build the images first:
//
//	make image fakes-image e2e
//
// REMORA_IMAGE and FAKES_IMAGE select other images. REMORA_E2E_KEEP=1 leaves
// the stack running after the suite.
package e2e

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const project = "remora-e2e"

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	if _, err := runCompose("up", "-d", "--wait", "--wait-timeout", "180"); err != nil {
		fmt.Fprintf(os.Stderr, "e2e: compose up: %v\n", err)
		dumpLogs()
		teardown()
		return 1
	}
	// The inference tests need providers, aliases and keys: apply them the way an operator would. Inside the
	// container the CLI reaches remora on the port of REMORA_LISTEN and signs in with REMORA_ADMIN_PASSWORD.
	if out, err := runCompose("exec", "-T", "remora", "remora", "apply", "-f", "/e2e/remora.yaml", "--yes"); err != nil {
		fmt.Fprintf(os.Stderr, "e2e: remora apply: %v\n%s\n", err, out)
		dumpLogs()
		teardown()
		return 1
	}
	code := m.Run()
	if code != 0 {
		dumpLogs()
	}
	teardown()
	return code
}

func dumpLogs() {
	out, err := runCompose("logs", "--no-color")
	fmt.Fprintf(os.Stderr, "e2e: docker compose logs:\n%s\n", out)
	if err != nil {
		fmt.Fprintf(os.Stderr, "e2e: compose logs: %v\n", err)
	}
}

func teardown() {
	if os.Getenv("REMORA_E2E_KEEP") == "1" {
		fmt.Fprintf(os.Stderr, "e2e: REMORA_E2E_KEEP=1, leaving project %s running\n", project)
		return
	}
	if _, err := runCompose("down", "-v"); err != nil {
		fmt.Fprintf(os.Stderr, "e2e: compose down: %v\n", err)
	}
}

// runCompose runs `docker compose -f compose.yaml -p remora-e2e <args>` and
// returns stdout. The error carries stderr.
func runCompose(args ...string) (string, error) {
	full := append([]string{"compose", "-f", "compose.yaml", "-p", project}, args...)
	// TestMain has no test context; the suite's own timeout bounds this.
	//nolint:gosec // G204: fixed docker binary; args come from the test suite itself, not external input.
	cmd := exec.CommandContext(context.Background(), "docker", full...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("docker %s: %w: %s", strings.Join(full, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// compose runs a docker compose command against the e2e project.
func compose(t testing.TB, args ...string) (string, error) {
	t.Helper()
	return runCompose(args...)
}

// lookupAddr returns the host address published for a service port.
func lookupAddr(service string, port int) (string, error) {
	out, err := runCompose("port", service, fmt.Sprint(port))
	if err != nil {
		return "", err
	}
	addr := strings.TrimSpace(out)
	if addr == "" || strings.HasSuffix(addr, ":0") {
		return "", fmt.Errorf("no published address for %s:%d (got %q)", service, port, addr)
	}
	return addr, nil
}

// hostAddr returns "127.0.0.1:NNNNN", the host side of a published port.
func hostAddr(t testing.TB, service string, port int) string {
	t.Helper()
	addr, err := lookupAddr(service, port)
	if err != nil {
		t.Fatalf("hostAddr %s:%d: %v", service, port, err)
	}
	return addr
}

func remoraURL(t testing.TB, path string) string {
	t.Helper()
	return "http://" + hostAddr(t, "remora", 8080) + path
}

func fakesURL(t testing.TB, path string) string {
	t.Helper()
	return "http://" + hostAddr(t, "fakes", 9090) + path
}

// execIn runs a command inside a running service and returns its stdout and
// exit code. It fails the test only when the command could not run at all.
func execIn(t testing.TB, service string, args ...string) (stdout string, code int) {
	t.Helper()
	full := append([]string{"compose", "-f", "compose.yaml", "-p", project, "exec", "-T", service}, args...)
	//nolint:gosec // G204: fixed docker binary; service and args come from the test suite itself, not external input.
	cmd := exec.CommandContext(t.Context(), "docker", full...)
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	err := cmd.Run()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return out.String(), 0
	case errors.As(err, &exitErr):
		return out.String(), exitErr.ExitCode()
	default:
		t.Fatalf("exec in %s %v: %v: %s", service, args, err, errOut.String())
		return "", -1
	}
}

// resetFakes clears every script and recording in the fakes.
func resetFakes(t testing.TB) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fakesURL(t, "/_control/reset"), nil)
	if err != nil {
		t.Fatalf("reset fakes: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("reset fakes: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("reset fakes: status %d, want 204", resp.StatusCode)
	}
}

// waitHealthy polls remora's /healthz until it answers 200. The published port
// is looked up on every attempt because a restart may change it.
func waitHealthy(t testing.TB, timeout time.Duration) {
	t.Helper()
	client := &http.Client{Timeout: 3 * time.Second}
	deadline := time.Now().Add(timeout)
	var last string
	for time.Now().Before(deadline) {
		addr, err := lookupAddr("remora", 8080)
		if err != nil {
			last = err.Error()
		} else {
			resp, err := healthz(t.Context(), client, "http://"+addr+"/healthz")
			if err != nil {
				last = err.Error()
			} else {
				_ = resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					return
				}
				last = fmt.Sprintf("status %d", resp.StatusCode)
			}
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("remora not healthy after %s: %s", timeout, last)
}

// healthz sends a GET to url with ctx.
func healthz(ctx context.Context, client *http.Client, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	return client.Do(req)
}
