package api

import (
	"context"

	"github.com/47vigen/remora/internal/api/gen"
)

// The operations below answer 501 not_implemented until their task implements them; each task deletes the
// methods it implements, and the file goes when the last one does.

// ListAccounts is not implemented yet.
func (s *Server) ListAccounts(context.Context, gen.ListAccountsRequestObject) (gen.ListAccountsResponseObject, error) {
	return nil, errNotImplemented
}

// ImportAccount is not implemented yet.
func (s *Server) ImportAccount(context.Context, gen.ImportAccountRequestObject) (gen.ImportAccountResponseObject, error) {
	return nil, errNotImplemented
}

// StartAccountLogin is not implemented yet.
func (s *Server) StartAccountLogin(context.Context, gen.StartAccountLoginRequestObject) (gen.StartAccountLoginResponseObject, error) {
	return nil, errNotImplemented
}

// CancelAccountLogin is not implemented yet.
func (s *Server) CancelAccountLogin(context.Context, gen.CancelAccountLoginRequestObject) (gen.CancelAccountLoginResponseObject, error) {
	return nil, errNotImplemented
}

// GetAccountLogin is not implemented yet.
func (s *Server) GetAccountLogin(context.Context, gen.GetAccountLoginRequestObject) (gen.GetAccountLoginResponseObject, error) {
	return nil, errNotImplemented
}

// CompleteAccountLogin is not implemented yet.
func (s *Server) CompleteAccountLogin(context.Context, gen.CompleteAccountLoginRequestObject) (gen.CompleteAccountLoginResponseObject, error) {
	return nil, errNotImplemented
}

// DeleteAccount is not implemented yet.
func (s *Server) DeleteAccount(context.Context, gen.DeleteAccountRequestObject) (gen.DeleteAccountResponseObject, error) {
	return nil, errNotImplemented
}

// UpdateAccount is not implemented yet.
func (s *Server) UpdateAccount(context.Context, gen.UpdateAccountRequestObject) (gen.UpdateAccountResponseObject, error) {
	return nil, errNotImplemented
}

// ResetAccountCooldown is not implemented yet.
func (s *Server) ResetAccountCooldown(context.Context, gen.ResetAccountCooldownRequestObject) (gen.ResetAccountCooldownResponseObject, error) {
	return nil, errNotImplemented
}

// ApplyConfig is not implemented yet.
func (s *Server) ApplyConfig(context.Context, gen.ApplyConfigRequestObject) (gen.ApplyConfigResponseObject, error) {
	return nil, errNotImplemented
}

// ExportConfig is not implemented yet.
func (s *Server) ExportConfig(context.Context, gen.ExportConfigRequestObject) (gen.ExportConfigResponseObject, error) {
	return nil, errNotImplemented
}

// PlanConfig is not implemented yet.
func (s *Server) PlanConfig(context.Context, gen.PlanConfigRequestObject) (gen.PlanConfigResponseObject, error) {
	return nil, errNotImplemented
}

// GetKeyUsage is not implemented yet.
func (s *Server) GetKeyUsage(context.Context, gen.GetKeyUsageRequestObject) (gen.GetKeyUsageResponseObject, error) {
	return nil, errNotImplemented
}

// ListLogs is not implemented yet.
func (s *Server) ListLogs(context.Context, gen.ListLogsRequestObject) (gen.ListLogsResponseObject, error) {
	return nil, errNotImplemented
}

// GetLog is not implemented yet.
func (s *Server) GetLog(context.Context, gen.GetLogRequestObject) (gen.GetLogResponseObject, error) {
	return nil, errNotImplemented
}

// GetStats is not implemented yet.
func (s *Server) GetStats(context.Context, gen.GetStatsRequestObject) (gen.GetStatsResponseObject, error) {
	return nil, errNotImplemented
}
