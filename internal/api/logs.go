package api

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"slices"

	"github.com/47vigen/remora/internal/api/gen"
	"github.com/47vigen/remora/internal/engine/bifrost"
)

// maxPathAttempts bounds how many fallback attempts of one request the detail reads.
const maxPathAttempts = 50

// ListLogs lists request log entries, newest first, in remora's names. A key or alias the front door does not know
// matches nothing.
func (s *Server) ListLogs(ctx context.Context, req gen.ListLogsRequestObject) (gen.ListLogsResponseObject, error) {
	if s.d.Logs == nil || s.d.Names == nil {
		return nil, errNotImplemented
	}
	f, err := s.toFilter(ctx, req.Params)
	if errors.Is(err, errNoMatch) {
		return gen.ListLogs200JSONResponse{Items: []gen.LogEntry{}, Total: 0}, nil
	}
	if err != nil {
		return nil, err
	}
	f.Limit, f.Offset = defaultLimit, 0
	if l := req.Params.Limit; l != nil && *l > 0 {
		f.Limit = min(*l, maxLimit)
	}
	if o := req.Params.Offset; o != nil && *o > 0 {
		f.Offset = *o
	}
	f.SortBy, f.Order = "timestamp", "desc"
	page, err := s.d.Logs.SearchLogs(ctx, f)
	if err != nil {
		return nil, err
	}
	// A fallback attempt carries no routing rule of its own; it takes its request's alias when the request is on the
	// same page.
	aliases := map[string]string{}
	for _, l := range page.Logs {
		if l.ParentRequestID == nil {
			aliases[l.ID] = s.ruleAlias(ctx, l.RoutingRuleID)
		}
	}
	items := make([]gen.LogEntry, 0, len(page.Logs))
	for _, l := range page.Logs {
		items = append(items, s.toEntry(ctx, l, aliases[deref(l.ParentRequestID)]))
	}
	return gen.ListLogs200JSONResponse{Items: items, Total: int(page.Total)}, nil
}

// GetLog answers one entry with its bodies and the request's whole fallback path, root first, whichever attempt
// is asked for.
func (s *Server) GetLog(ctx context.Context, req gen.GetLogRequestObject) (gen.GetLogResponseObject, error) {
	if s.d.Logs == nil || s.d.Names == nil {
		return nil, errNotImplemented
	}
	l, err := s.d.Logs.GetLog(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	root := l
	if p := deref(l.ParentRequestID); p != "" && p != l.ID {
		if root, err = s.d.Logs.GetLog(ctx, p); err != nil {
			return nil, err
		}
	}
	path, err := s.fallbackPath(ctx, root, l)
	if err != nil {
		return nil, err
	}
	rootAlias := s.ruleAlias(ctx, root.RoutingRuleID)
	e := s.toEntry(ctx, l, rootAlias)
	d := gen.GetLog200JSONResponse{
		Id: e.Id, ParentId: e.ParentId, Attempt: e.Attempt, Time: e.Time, Key: e.Key, Alias: e.Alias,
		Provider: e.Provider, Model: e.Model, Status: gen.LogDetailStatus(e.Status), LatencyMs: e.LatencyMs,
		Tokens: e.Tokens, CostUsd: e.CostUsd, Stream: e.Stream, Error: e.Error,
		Request:      bodies(map[string]json.RawMessage{"messages": l.InputHistory, "params": l.Params}),
		Response:     bodies(map[string]json.RawMessage{"message": l.OutputMessage}),
		FallbackPath: make([]gen.LogEntry, 0, len(path)),
	}
	for _, a := range path {
		d.FallbackPath = append(d.FallbackPath, s.toEntry(ctx, a, rootAlias))
	}
	return d, nil
}

// fallbackPath returns every attempt of root's request ordered by attempt, with asked (already read whole) in its
// place. The list leaves error details out, so a failed attempt is read on its own for its error.
func (s *Server) fallbackPath(ctx context.Context, root, asked bifrost.Log) ([]bifrost.Log, error) {
	page, err := s.d.Logs.SearchLogs(ctx, bifrost.LogFilter{
		ParentRequestID: root.ID, SortBy: "timestamp", Order: "asc", Limit: maxPathAttempts,
	})
	if err != nil {
		return nil, err
	}
	path := []bifrost.Log{root}
	for _, a := range page.Logs {
		switch {
		case a.ID == root.ID:
			continue
		case a.ID == asked.ID:
			a = asked
		case a.Status == "error" && len(a.ErrorDetails) == 0:
			full, err := s.d.Logs.GetLog(ctx, a.ID)
			if err != nil && !errors.Is(err, bifrost.ErrNotFound) {
				return nil, err
			}
			if err == nil {
				a = full
			}
		}
		path = append(path, a)
	}
	slices.SortStableFunc(path, func(x, y bifrost.Log) int { return cmp.Compare(x.FallbackIndex, y.FallbackIndex) })
	return path, nil
}

// bodies keeps the non-empty fields of m, or answers nil when none is set.
func bodies(m map[string]json.RawMessage) *map[string]any {
	out := map[string]any{}
	for k, v := range m {
		if len(v) > 0 && string(v) != "null" {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return &out
}
