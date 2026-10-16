package alias

import (
	"strconv"
	"strings"
)

// thinkingLevels are the named levels a member may carry. The OAuth engine also accepts xhigh, max, none and auto;
// they stay out until someone asks.
var thinkingLevels = map[string]bool{"minimal": true, "low": true, "medium": true, "high": true}

// ParseThinking splits a member ref "<provider>/<model>(<level>)" into the ref without the suffix and the level, the
// way the OAuth engine reads it: the last "(" of a ref that ends in ")". level is "" when the ref has no suffix. ok is
// false when a suffix is present but is not one of minimal, low, medium, high, or a whole number of thinking tokens
// (0 = off).
func ParseThinking(ref string) (base, level string, ok bool) {
	open := strings.LastIndexByte(ref, '(')
	if open < 0 || !strings.HasSuffix(ref, ")") {
		return ref, "", true
	}
	base, level = ref[:open], ref[open+1:len(ref)-1]
	if thinkingLevels[level] {
		return base, level, true
	}
	n, err := strconv.Atoi(level)
	return base, level, err == nil && n >= 0 && level[0] != '+'
}
