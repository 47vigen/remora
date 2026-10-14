package config

import (
	"context"
	"fmt"
)

// ApplyError is the failure of one change during Apply.
type ApplyError struct {
	Change Change
	Err    error
}

func (e *ApplyError) Error() string {
	return fmt.Sprintf("%s %s %s: %v", e.Change.Op, e.Change.Kind, e.Change.Name, e.Err)
}

func (e *ApplyError) Unwrap() error { return e.Err }

// Apply makes the store match want, change by change in Plan's order (see Plan for matching, secrets and prune),
// and returns the changes it made. It stops at the first failure and returns the changes made before it with an
// *ApplyError naming the change that failed. A client key created without a value gets a generated one, returned
// once in its Change's Value.
func (s *Service) Apply(ctx context.Context, want File, sec Secrets, prune bool) ([]Change, error) {
	steps, err := s.plan(ctx, want, sec, prune)
	if err != nil {
		return []Change{}, err
	}
	applied := make([]Change, 0, len(steps))
	for _, st := range steps {
		v, err := st.run(ctx)
		if err != nil {
			return applied, &ApplyError{Change: st.Change, Err: err}
		}
		st.Value = v
		applied = append(applied, st.Change)
	}
	return applied, nil
}
