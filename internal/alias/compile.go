package alias

import (
	"errors"
	"fmt"
	"strings"
)

// DefaultMaxDepth is the nesting limit used when Compile is given maxDepth <= 0.
const DefaultMaxDepth = 5

// Compile errors. Returned errors wrap one of these and name the alias path.
var (
	ErrCycle         = errors.New("alias: cycle")
	ErrDepth         = errors.New("alias: nesting too deep")
	ErrUnknownMember = errors.New("alias: unknown member")
	ErrEmpty         = errors.New("alias: no members")
)

// Compile expands the alias name into a flat, ordered chain of provider targets. Nested aliases are expanded depth
// first in member order, and a target already in the chain is dropped. A member ref is an alias when all has that
// key, otherwise "<provider>/<model>" split at the first "/". The named alias is depth 1; maxDepth <= 0 means
// DefaultMaxDepth.
func Compile(all map[string]Alias, name string, maxDepth int) ([]Target, error) {
	if maxDepth <= 0 {
		maxDepth = DefaultMaxDepth
	}
	c := compiler{all: all, maxDepth: maxDepth, onPath: map[string]bool{}, seen: map[Target]bool{}}
	if _, ok := all[name]; !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownMember, name)
	}
	if err := c.expand(name, nil); err != nil {
		return nil, err
	}
	return c.out, nil
}

type compiler struct {
	all      map[string]Alias
	maxDepth int
	onPath   map[string]bool
	seen     map[Target]bool
	out      []Target
}

func (c *compiler) expand(name string, path []string) error {
	path = append(path[:len(path):len(path)], name)
	if c.onPath[name] {
		return fmt.Errorf("%w: %s", ErrCycle, strings.Join(path, " -> "))
	}
	if len(path) > c.maxDepth {
		return fmt.Errorf("%w: %s (limit %d)", ErrDepth, strings.Join(path, " -> "), c.maxDepth)
	}
	al := c.all[name]
	if len(al.Members) == 0 {
		return fmt.Errorf("%w: %s", ErrEmpty, strings.Join(path, " -> "))
	}
	c.onPath[name] = true
	defer delete(c.onPath, name)
	for _, m := range al.Members {
		if _, ok := c.all[m.Ref]; ok {
			if err := c.expand(m.Ref, path); err != nil {
				return err
			}
			continue
		}
		provider, model, ok := strings.Cut(m.Ref, "/")
		if !ok || provider == "" || model == "" {
			return fmt.Errorf("%w: %q in %s", ErrUnknownMember, m.Ref, strings.Join(path, " -> "))
		}
		t := Target{Provider: provider, Model: model}
		if !c.seen[t] {
			c.seen[t] = true
			c.out = append(c.out, t)
		}
	}
	return nil
}
