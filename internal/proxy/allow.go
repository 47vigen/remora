package proxy

import (
	"path"
	"strings"
)

// Allowed reports whether model, as the client wrote it (an alias name or "<provider>/<model>"), matches one of a
// key's allowlist patterns. The pattern "*" matches every name, a pattern ending in "/*" matches any depth below its
// prefix ("openrouter/*" matches "openrouter/qwen/qwen3-embedding"), and every other pattern follows path.Match.
// Matching is byte for byte and case-sensitive; a malformed pattern never matches, and no patterns deny everything.
func Allowed(patterns []string, model string) bool {
	if model == "" {
		return false
	}
	for _, p := range patterns {
		if matches(p, model) {
			return true
		}
	}
	return false
}

func matches(pattern, model string) bool {
	if pattern == "*" {
		return true
	}
	if prefix, ok := strings.CutSuffix(pattern, "/*"); ok {
		head, rest, found := strings.Cut(model, "/")
		// The prefix itself may hold a slash or a glob ("org/team/*", "up-?/*"), so match it against as many
		// leading segments as it has.
		if n := strings.Count(prefix, "/"); n > 0 {
			segs := strings.SplitN(model, "/", n+2)
			if len(segs) < n+2 {
				return false
			}
			head, rest, found = strings.Join(segs[:n+1], "/"), segs[n+1], true
		}
		if !found || rest == "" {
			return false
		}
		ok, err := path.Match(prefix, head)
		return err == nil && ok
	}
	ok, err := path.Match(pattern, model)
	return err == nil && ok
}
