package proxy

import "testing"

func TestAllowed(t *testing.T) {
	cases := []struct {
		patterns []string
		model    string
		want     bool
	}{
		{[]string{"fast"}, "fast", true},
		{[]string{"fast"}, "fast2", false},
		{[]string{"fast"}, "Fast", false},
		{[]string{"*"}, "memory/fast", true},
		{[]string{"*"}, "openrouter/qwen/qwen3-embedding", true},
		{[]string{"up-a/*"}, "up-a/m1", true},
		{[]string{"openrouter/*"}, "openrouter/qwen/qwen3-embedding", true},
		{[]string{"up-a/*"}, "up-b/m1", false},
		{[]string{"up-a/*"}, "up-a/", false},
		{[]string{"up-a/*"}, "up-a", false},
		{[]string{"up-a/*"}, "up-ab/m1", false},
		{[]string{"openrouter/qwen/*"}, "openrouter/qwen/a/b", true},
		{[]string{"openrouter/qwen/*"}, "openrouter/qwen", false},
		{[]string{"openrouter/qwen/*"}, "openrouter/meta/a", false},
		{[]string{"up-?/*"}, "up-a/m1/x", true},
		{[]string{"[/*"}, "[/m1", false},
		{[]string{"gemini-*"}, "gemini-3.8-flash", true},
		{[]string{"gemini-*"}, "gemini-x/y", false},
		{[]string{"memory/*"}, "memory/fast", true},
		{[]string{"m?/x"}, "m1/x", true},
		{[]string{"["}, "[", false}, // malformed pattern never matches
		{[]string{"[", "fast"}, "fast", true},
		{[]string{"fast", "slow"}, "slow", true},
		{[]string{"می\u200cشود"}, "میشود", false}, // ZWNJ compared byte for byte
		{[]string{"*"}, "", false},
		{nil, "fast", false},        // no entries denies everything
		{[]string{}, "fast", false}, // no entries denies everything
	}
	for _, c := range cases {
		if got := Allowed(c.patterns, c.model); got != c.want {
			t.Errorf("Allowed(%q, %q) = %v, want %v", c.patterns, c.model, got, c.want)
		}
	}
}
