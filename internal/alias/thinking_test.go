package alias

import (
	"reflect"
	"testing"
)

func TestParseThinking(t *testing.T) {
	const m = "antigravity/gemini-3-flash"
	cases := []struct {
		ref, base, level string
		ok               bool
	}{
		{m + "(low)", m, "low", true},
		{m + "(minimal)", m, "minimal", true},
		{m + "(medium)", m, "medium", true},
		{m + "(high)", m, "high", true},
		{m + "(1024)", m, "1024", true},
		{m + "(0)", m, "0", true},
		{m, m, "", true},
		{m + "(extreme)", m, "extreme", false},
		{m + "(-1)", m, "-1", false},
		{m + "()", m, "", false},
		{m + "(1.5)", m, "1.5", false},
		{"antigravity/foo(bar)baz", "antigravity/foo(bar)baz", "", true},
	}
	for _, c := range cases {
		base, level, ok := ParseThinking(c.ref)
		if base != c.base || level != c.level || ok != c.ok {
			t.Errorf("ParseThinking(%q) = %q, %q, %v; want %q, %q, %v", c.ref, base, level, ok, c.base, c.level, c.ok)
		}
	}
}

func TestCompileKeepsThinkingSuffix(t *testing.T) {
	all := index([]Alias{a("fast", "antigravity/x(low)", "antigravity/x")})
	got, err := Compile(all, "fast", 0)
	if err != nil {
		t.Fatal(err)
	}
	want := []Target{{Provider: "antigravity", Model: "x(low)"}, {Provider: "antigravity", Model: "x"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Compile = %+v, want %+v", got, want)
	}
}
