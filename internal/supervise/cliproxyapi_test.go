package supervise

import (
	"os"
	"testing"

	"go.yaml.in/yaml/v3"
)

// TestCLIProxyAPIEngineRendersSettings: the settings accounts.Reconcile owns are in the file the engine loads at
// start, so they hold even when a management write lands in the engine's blind window right after it boots.
func TestCLIProxyAPIEngineRendersSettings(t *testing.T) {
	dir := t.TempDir()
	if _, err := CLIProxyAPIEngine(testConfig(dir), testCreds); err != nil {
		t.Fatalf("CLIProxyAPIEngine: %v", err)
	}
	raw, err := os.ReadFile(dir + "/cliproxyapi/config.yaml") //nolint:gosec // G304: a path under t.TempDir().
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode config.yaml: %v", err)
	}
	get := func(path ...string) any {
		var cur any = doc
		for _, p := range path {
			m, ok := cur.(map[string]any)
			if !ok {
				return nil
			}
			cur = m[p]
		}
		return cur
	}
	checks := []struct {
		path []string
		want any
	}{
		{[]string{"routing", "strategy"}, "round-robin"},
		{[]string{"routing", "force-model-prefix"}, true},
		{[]string{"routing", "cooldown", "save-cooldown-status"}, true},
		{[]string{"upstream", "codex", "model-level-cooling"}, true},
	}
	for _, c := range checks {
		if got := get(c.path...); got != c.want {
			t.Errorf("%v = %#v, want %#v", c.path, got, c.want)
		}
	}
}
