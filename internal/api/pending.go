package api

import (
	"context"

	"github.com/47vigen/remora/internal/api/gen"
)

// The operations below answer 501 not_implemented until their plan 05 task implements them (Tasks 8–11: logs,
// stats and key usage). Each task deletes the methods it implements, and the file goes when the last one does;
// config_test.go's pendingOperations lists the same methods.

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
