//go:build engine

package quota

import (
	"os"
	"regexp"
	"testing"
)

// TestUserAgentsMatchEngine fails when an engine bump changes a user agent, the Antigravity manifest or its floor,
// so remora's quota checks keep looking like the engine's own traffic.
func TestUserAgentsMatchEngine(t *testing.T) {
	const root = "../../engines/cliproxyapi/"
	read := func(file string) string {
		t.Helper()
		b, err := os.ReadFile(root + file) //nolint:gosec // G304: fixed paths in the engine submodule
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		return string(b)
	}
	capture := func(src, name string) string {
		t.Helper()
		m := regexp.MustCompile(name + `\s*=\s*"([^"]+)"`).FindStringSubmatch(src)
		if m == nil {
			t.Fatalf("%s not found", name)
		}
		return m[1]
	}
	version := read("internal/misc/antigravity_version.go")
	for _, tt := range []struct{ src, name, want string }{
		{read("internal/runtime/executor/codex_executor_request.go"), "codexUserAgent", codexUserAgent},
		{read("internal/runtime/executor/helps/claude_device_profile.go"), "defaultClaudeFingerprintUserAgent", claudeUserAgent},
		{version, "antigravityFallbackVersion", antigravityVersionFloor},
		{version, "antigravityHubPlatform", antigravityPlatform},
		{version, "antigravityHubLatestManifestURL", antigravityManifestURL},
	} {
		if got := capture(tt.src, tt.name); got != tt.want {
			t.Errorf("engine %s = %q, remora has %q", tt.name, got, tt.want)
		}
	}
	if !regexp.MustCompile(`antigravityVersionCacheTTL\s*=\s*6 \* time\.Hour`).MatchString(version) {
		t.Error("engine antigravityVersionCacheTTL is no longer 6h; revisit antigravityVersionTTL")
	}
}
