package alias

import (
	"errors"
	"fmt"
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

// CheckThinking refuses a member ref whose thinking suffix is not a valid level, or that carries one on a provider
// isSubscription does not name: there it would reach the provider as an unknown model. Alias names pass.
func CheckThinking(ref string, isSubscription func(provider string) bool) error {
	provider, _, isTarget := strings.Cut(ref, "/")
	if !isTarget {
		return nil
	}
	_, level, ok := ParseThinking(ref)
	switch {
	case !ok:
		return fmt.Errorf("thinking level must be minimal, low, medium, high or a token budget; got %q", level)
	case level != "" && !isSubscription(provider):
		return errors.New("a thinking level applies only to subscription models")
	}
	return nil
}
