package alias

import (
	"strings"
	"testing"
)

func TestValidName(t *testing.T) {
	for _, name := range []string{"fast", "memory/fast", "gpt-6-luna", "a.b:c", strings.Repeat("a", 128)} {
		if err := ValidName(name); err != nil {
			t.Errorf("ValidName(%q) = %v, want nil", name, err)
		}
	}
	for _, name := range []string{"", "/x", "x y", "é", strings.Repeat("a", 129), "-x", "x\n"} {
		if err := ValidName(name); err == nil {
			t.Errorf("ValidName(%q) = nil, want an error", name)
		}
	}
}
