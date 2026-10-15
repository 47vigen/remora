package alias

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func a(name string, refs ...string) Alias {
	al := Alias{Name: name, Kind: KindChat}
	for _, r := range refs {
		al.Members = append(al.Members, Member{Ref: r})
	}
	return al
}

// chain returns c1 -> c2 -> ... -> cn -> up-a/m1.
func chain(n int) []Alias {
	out := make([]Alias, 0, n)
	for i := 1; i < n; i++ {
		out = append(out, a(fmt.Sprintf("c%d", i), fmt.Sprintf("c%d", i+1)))
	}
	return append(out, a(fmt.Sprintf("c%d", n), "up-a/m1"))
}

func index(all []Alias) map[string]Alias {
	m := make(map[string]Alias, len(all))
	for _, al := range all {
		m[al.Name] = al
	}
	return m
}

func TestCompile(t *testing.T) {
	cases := []struct {
		name  string
		all   []Alias
		root  string
		depth int
		want  []Target
		err   error
	}{
		{"flat", []Alias{a("fast", "up-a/m1", "up-b/m1")}, "fast", 0, []Target{{"up-a", "m1"}, {"up-b", "m1"}}, nil},
		{
			"nested in order",
			[]Alias{a("memory/fast", "inner", "up-c/m1"), a("inner", "up-a/m1", "up-b/m1")},
			"memory/fast", 0,
			[]Target{{"up-a", "m1"}, {"up-b", "m1"}, {"up-c", "m1"}},
			nil,
		},
		{
			"dedupe keeps first",
			[]Alias{a("x", "up-a/m1", "y", "up-b/m1"), a("y", "up-b/m1", "up-a/m1")},
			"x", 0,
			[]Target{{"up-a", "m1"}, {"up-b", "m1"}},
			nil,
		},
		{
			"model with slashes",
			[]Alias{a("emb", "openrouter/qwen/qwen3-embedding")},
			"emb", 0,
			[]Target{{"openrouter", "qwen/qwen3-embedding"}},
			nil,
		},
		{"self cycle", []Alias{a("x", "x")}, "x", 0, nil, ErrCycle},
		{"long cycle", []Alias{a("x", "y"), a("y", "z"), a("z", "x")}, "x", 0, nil, ErrCycle},
		{
			"diamond is not a cycle",
			[]Alias{a("x", "y", "z"), a("y", "w"), a("z", "w"), a("w", "up-a/m1")},
			"x", 0,
			[]Target{{"up-a", "m1"}},
			nil,
		},
		{"depth 5 ok", chain(5), "c1", 0, []Target{{"up-a", "m1"}}, nil},
		{"depth 6 rejected", chain(6), "c1", 0, nil, ErrDepth},
		{"custom depth", chain(3), "c1", 2, nil, ErrDepth},
		{"unknown member", []Alias{a("x", "nope")}, "x", 0, nil, ErrUnknownMember},
		{"unknown root", nil, "x", 0, nil, ErrUnknownMember},
		{"empty", []Alias{a("x")}, "x", 0, nil, ErrEmpty},
		{"empty nested", []Alias{a("x", "y"), a("y")}, "x", 0, nil, ErrEmpty},
		{"half ref", []Alias{a("x", "up-a/")}, "x", 0, nil, ErrUnknownMember},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Compile(index(c.all), c.root, c.depth)
			if !errors.Is(err, c.err) {
				t.Fatalf("Compile err = %v, want %v", err, c.err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("Compile = %v, want %v", got, c.want)
			}
		})
	}
}

func TestCompile_ErrorNamesPath(t *testing.T) {
	_, err := Compile(index([]Alias{a("fast", "inner"), a("inner", "fast")}), "fast", 0)
	if err == nil || !strings.Contains(err.Error(), "fast -> inner -> fast") {
		t.Errorf("err = %v, want it to name the path fast -> inner -> fast", err)
	}
}

func TestStrategyValid(t *testing.T) {
	for _, s := range []Strategy{StrategyFallback, StrategyRoundRobin} {
		if !s.Valid() {
			t.Errorf("%q.Valid() = false, want true", s)
		}
	}
	for _, s := range []Strategy{"fallback", "priority", "Round-Robin"} {
		if s.Valid() {
			t.Errorf("%q.Valid() = true, want false", s)
		}
	}
}
