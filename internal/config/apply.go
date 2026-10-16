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

// PlanMismatchError is ApplyPlanned's answer when the store no longer plans to the wanted hash: nothing was applied.
type PlanMismatchError struct {
	Changes []Change // the fresh plan
	Hash    string   // PlanHash over the same inputs and the current store
}

func (e *PlanMismatchError) Error() string {
	return "the configuration changed since the plan; nothing was applied"
}

// errNoWrite ends a batch that must write nothing: an empty plan or a plan hash mismatch. It rolls the batch back
// before the reconcile pass, so the engines are not touched.
var errNoWrite = errors.New("config: nothing to write")

// Apply makes the store match want, change by change in Plan's order (see Plan for matching, secrets and prune),
// and returns the changes it made. The changes are made in one Batch: when one fails, none is kept, and the error is
// an *ApplyError naming the change that failed. A client key created without a value gets a generated one, returned
// once in its Change's Value.
func (s *Service) Apply(ctx context.Context, want File, sec Secrets, prune bool) ([]Change, error) {
	return s.ApplyPlanned(ctx, want, sec, prune, "")
}

// ApplyPlanned is Apply with a guard: when wantHash is not "", the plan is made inside the apply's Batch, which holds
// the store's write lock until commit, and if its PlanHash differs nothing is written and the error is a
// *PlanMismatchError.
func (s *Service) ApplyPlanned(ctx context.Context, want File, sec Secrets, prune bool, wantHash string) ([]Change, error) {
	var (
		applied  []Change
		mismatch *PlanMismatchError
		planErr  error // returned as is, not wrapped by the batch
	)
	err := s.batch(ctx, func(ctx context.Context) error {
		steps, err := s.plan(ctx, want, sec, prune)
		if err != nil {
			planErr = err
			return errNoWrite
		}
		if wantHash != "" {
			cur, err := s.Export(ctx)
			if err != nil {
				planErr = err
				return errNoWrite
			}
			if h := PlanHash(prune, want, sec, cur); h != wantHash {
				mismatch = &PlanMismatchError{Changes: make([]Change, len(steps)), Hash: h}
				for i, st := range steps {
					mismatch.Changes[i] = st.Change
				}
				return errNoWrite
			}
		}
		if len(steps) == 0 {
			return errNoWrite
		}
		applied = make([]Change, 0, len(steps))
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
	if planErr != nil {
		return []Change{}, planErr
	}
	if mismatch != nil {
		return []Change{}, mismatch
	}
	if errors.Is(err, errNoWrite) {
		return []Change{}, nil
	}
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
