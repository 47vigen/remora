package accounts

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/47vigen/remora/internal/alias"
	"github.com/47vigen/remora/internal/engine/cliproxyapi"
	"github.com/47vigen/remora/internal/store"
)

// outReasons are the cooldown reasons that take an account out of the pool until someone signs it in again. A
// cooldown for quota or a transient error ends by itself and is not out.
var outReasons = []string{"invalid_grant", "unauthorized", "payment_required"}

// OutAccount is an enabled account the engine cannot use. Reason is "missing" (no credential), "signed out",
// MsgNoProject, or the engine's scrubbed status message.
type OutAccount struct {
	ID            int64
	Label, Reason string
}

// PoolStatus is one subscription provider's pool: how many accounts are enabled and which of them are out.
type PoolStatus struct {
	Provider string
	Enabled  int
	Out      []OutAccount
}

// Pool reports, per subscription provider with at least one enabled account, which enabled accounts are out of the
// pool: the credential is missing, or it is unavailable without a cooldown (a refresh that failed for good), or a
// cooldown's reason says it was signed out or refused payment, or the provider assigned it no project. One engine
// call, no model listing.
func (s *Service) Pool(ctx context.Context) ([]PoolStatus, error) {
	rows, err := s.store.Accounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("accounts: pool: %w", err)
	}
	creds, err := s.credentials(ctx)
	if err != nil {
		return nil, fmt.Errorf("accounts: pool: %w", err)
	}
	var out []PoolStatus
	for _, row := range rows {
		if !row.Enabled {
			continue
		}
		i := slices.IndexFunc(out, func(p PoolStatus) bool { return p.Provider == row.Provider })
		if i < 0 {
			out = append(out, PoolStatus{Provider: row.Provider})
			i = len(out) - 1
		}
		out[i].Enabled++
		if reason, isOut := outReason(creds, row.Provider, row.Credential); isOut {
			out[i].Out = append(out[i].Out, OutAccount{ID: row.ID, Label: row.Label, Reason: reason})
		}
	}
	return out, nil
}

// LoadAvailability takes the first availability snapshot before aliases are compiled at startup.
func (s *Service) LoadAvailability(ctx context.Context) error {
	rows, err := s.store.Accounts(ctx)
	if err != nil {
		return fmt.Errorf("accounts: read availability: %w", err)
	}
	creds, err := s.credentials(ctx)
	if err != nil {
		return fmt.Errorf("accounts: read availability: %w", err)
	}
	s.updateAvailability(rows, creds)
	return nil
}

// ObserveAvailability updates the provider set from the quota worker's auth-list poll. It reports any change.
func (s *Service) ObserveAvailability(rows []store.Account, creds []cliproxyapi.Credential) bool {
	return s.updateAvailability(rows, indexCredentials(creds))
}

func (s *Service) updateAvailability(rows []store.Account, creds map[string]cliproxyapi.Credential) bool {
	available := map[string]bool{}
	for _, row := range rows {
		if !row.Enabled || available[row.Provider] {
			continue
		}
		p, ok := cliproxyapi.ProviderByName(row.Provider)
		c := creds[row.Credential]
		if !ok || c.Disabled || c.Provider != p.Engine {
			continue
		}
		if _, out := outReason(creds, row.Provider, row.Credential); !out {
			available[row.Provider] = true
		}
	}
	prev, _ := s.available.Swap(available).(map[string]bool)
	return prev == nil || !maps.Equal(prev, available)
}

// Available removes subscription targets with no usable account and reports their providers. An empty result
// lets the reconciler keep the original chain. An unreadable auth list never replaces the last snapshot.
func (s *Service) Available(chain []alias.Target) ([]alias.Target, []string) {
	available, _ := s.available.Load().(map[string]bool)
	if available == nil {
		return chain, nil
	}
	var skipped []string
	kept := slices.DeleteFunc(chain, func(t alias.Target) bool {
		if cliproxyapi.IsSubscription(t.Provider) && !available[t.Provider] {
			skipped = append(skipped, t.Provider)
			return true
		}
		return false
	})
	return kept, skipped
}

// outReason says why the credential named name keeps its account of provider out of the pool, if it does.
func outReason(creds map[string]cliproxyapi.Credential, provider, name string) (string, bool) {
	c, ok := creds[name]
	switch {
	case !ok:
		return statusMissing, true
	case c.Unavailable && len(c.Cooldowns) == 0 && !c.Disabled:
		if msg := scrub(c.StatusMessage); msg != "" {
			return msg, true
		}
		return "signed out", true
	case NoProject(provider, c) && !c.Disabled:
		return MsgNoProject, true
	}
	for _, cd := range c.Cooldowns {
		if slices.Contains(outReasons, cd.Reason) {
			return "signed out", true
		}
	}
	return "", false
}
