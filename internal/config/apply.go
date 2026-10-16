package config

import (
	"context"
	"errors"
	"fmt"
	"slices"
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
// and returns the changes it made. The changes are made in one Batch: when one fails, none is kept, and the error is
// an *ApplyError naming the change that failed. A client key created without a value gets a generated one, returned
// once in its Change's Value.
func (s *Service) Apply(ctx context.Context, want File, sec Secrets, prune bool) ([]Change, error) {
	steps, err := s.plan(ctx, want, sec, prune)
	if err != nil {
		return []Change{}, err
	}
	if len(steps) == 0 {
		return []Change{}, nil
	}
	applied := make([]Change, 0, len(steps))
	err = s.batch(ctx, func(ctx context.Context) error {
		for _, st := range steps {
			v, err := st.run(ctx)
			if err != nil {
				return &ApplyError{Change: st.Change, Err: err}
			}
			st.Value = v
			applied = append(applied, st.Change)
		}
		return nil
	})
	var ae *ApplyError
	if errors.As(err, &ae) {
		return []Change{}, ae
	}
	if err != nil {
		return []Change{}, err
	}
	// The engine follows the committed strategies through the accounts reconciler, which retries on its own loop, so
	// an engine failure here never turns a committed apply into an error.
	if s.as != nil && slices.ContainsFunc(applied, func(c Change) bool { return c.Kind == "account_strategy" }) {
		_ = s.as.Reconcile(ctx)
	}
	return applied, nil
}
