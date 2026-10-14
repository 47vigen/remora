package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestApplyStagesEverything runs apply in a scratch repository: a plain `git commit` after it must record the gitlink
// and the matching versions.env line together.
func TestApplyStagesEverything(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	t.Chdir(dir)
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.CommandContext(t.Context(), "git", args...).CombinedOutput() //nolint:gosec // fixed git subcommands in a test
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	git("init", "-q")
	if err := os.MkdirAll(filepath.Join(dir, "engines"), 0o750); err != nil {
		t.Fatal(err)
	}
	env := "BIFROST_VERSION=transports/v2.3.0\nCLIPROXYAPI_VERSION=v8.0.28\n"
	if err := os.WriteFile(versionsFile, []byte(env), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	// The superproject already pins the engine, as in the real repository.
	git("update-index", "--add", "--cacheinfo", "160000,"+strings.Repeat("b", 40)+",engines/cliproxyapi")
	git("-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-q", "-m", "init")

	e, _ := engineByName("cliproxyapi")
	a := Action{Kind: KindBump, Engine: e, To: Tag{Name: "v8.1.0", SHA: strings.Repeat("a", 40)}}
	if err := apply(t.Context(), a, ""); err != nil {
		t.Fatal(err)
	}
	staged := strings.Fields(git("diff", "--cached", "--name-only"))
	for _, want := range []string{"engines/cliproxyapi", versionsFile} {
		if !slices.Contains(staged, want) {
			t.Errorf("staged %v, want %s among them", staged, want)
		}
	}
	if got := git("show", ":"+versionsFile); !strings.Contains(got, "CLIPROXYAPI_VERSION=v8.1.0") {
		t.Errorf("staged versions.env = %q", got)
	}
}
