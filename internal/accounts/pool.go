package accounts

import (
	"context"
	"fmt"
	"slices"

	"github.com/47vigen/remora/internal/engine/cliproxyapi"
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
