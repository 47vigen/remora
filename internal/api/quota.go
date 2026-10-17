package api

import (
	"context"

	"github.com/47vigen/remora/internal/api/gen"
	"github.com/47vigen/remora/internal/quota"
	"github.com/47vigen/remora/internal/store"
)

func toGenQuota(q store.AccountQuota) *gen.QuotaSnapshot {
	out := &gen.QuotaSnapshot{
		Plan: q.Plan, LimitReached: q.LimitReached, Source: gen.QuotaSnapshotSource(q.Source), CheckedAt: q.CheckedAt,
		Error: q.Error, Windows: make([]gen.QuotaWindow, 0, len(q.Windows)),
	}
	if !q.FetchedAt.IsZero() {
		out.FetchedAt = &q.FetchedAt
	}
	for _, w := range q.Windows {
		out.Windows = append(out.Windows, gen.QuotaWindow{Scope: w.Scope, Window: w.Window, Remaining: w.Remaining, ResetsAt: w.ResetsAt})
	}
	return out
}

// GetQuota answers every subscription account's quota, grouped by provider.
func (s *Server) GetQuota(ctx context.Context, _ gen.GetQuotaRequestObject) (gen.GetQuotaResponseObject, error) {
	if s.d.Quota == nil {
		return nil, errNotImplemented
	}
	views, err := s.d.Quota.Overview(ctx)
	if err != nil {
		return nil, err
	}
	resp := gen.GetQuota200JSONResponse{}
	for _, v := range views {
		p := gen.QuotaProvider{
			Provider: v.Provider, Supported: v.Supported, Available: v.Available, Exhausted: v.Exhausted,
			Cooling: v.Cooling, SignIn: v.SignIn, Suspended: v.Suspended, NextReset: v.NextReset, Accounts: make([]gen.QuotaAccount, 0, len(v.Accounts)),
		}
		for _, a := range v.Accounts {
			qa := gen.QuotaAccount{Account: toGenAccount(toAccount(a.Account))}
			if a.Quota != nil {
				qa.Quota = toGenQuota(*a.Quota)
			}
			p.Accounts = append(p.Accounts, qa)
		}
		resp = append(resp, p)
	}
	return resp, nil
}

// RefreshQuota queues quota checks of one account or one provider.
func (s *Server) RefreshQuota(ctx context.Context, req gen.RefreshQuotaRequestObject) (gen.RefreshQuotaResponseObject, error) {
	if s.d.Quota == nil {
		return nil, errNotImplemented
	}
	if req.Body == nil {
		return nil, quota.ErrRefreshTarget
	}
	queued, skipped, err := s.d.Quota.Refresh(ctx, deref(req.Body.AccountId), deref(req.Body.Provider))
	if err != nil {
		return nil, err
	}
	return gen.RefreshQuota202JSONResponse{Queued: append([]string{}, queued...), Skipped: append([]string{}, skipped...)}, nil
}
