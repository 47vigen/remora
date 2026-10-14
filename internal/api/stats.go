package api

import (
	"context"
	"errors"
	"math"
	"slices"
	"time"

	"github.com/47vigen/remora/internal/api/gen"
	"github.com/47vigen/remora/internal/engine/bifrost"
)

// percentile is the p-quantile (0 < p ≤ 1) of the latency of the successful attempts f matches, by nearest rank:
// it counts them, then reads the one row at rank ceil(p·n) from the list sorted by latency. It is nil when nothing
// matches. The front door's log store keeps no median, so this is exact rather than read from a histogram.
func percentile(ctx context.Context, src LogSource, f bifrost.LogFilter, p float64) (*float64, error) {
	f.Status, f.Limit, f.Offset, f.SortBy, f.Order = []string{"success"}, 1, 0, "", ""
	count, err := src.SearchLogs(ctx, f)
	if err != nil {
		return nil, err
	}
	n := count.Total
	if n == 0 {
		return nil, nil
	}
	rank := int64(math.Ceil(p*float64(n) - 1e-9))
	f.SortBy, f.Order, f.Offset = "latency", "asc", int(min(max(rank, 1), n)-1)
	row, err := src.SearchLogs(ctx, f)
	if err != nil {
		return nil, err
	}
	if len(row.Logs) == 0 || row.Logs[0].Latency == nil {
		return nil, nil
	}
	return new(*row.Logs[0].Latency), nil
}

// summary is the stats of what f matches. Requests and errors count a fallback chain once, as the user saw it.
func (s *Server) summary(ctx context.Context, f bifrost.LogFilter) (gen.StatsSummary, error) {
	st, err := s.d.Logs.LogStats(ctx, f)
	if err != nil {
		return gen.StatsSummary{}, err
	}
	out := gen.StatsSummary{
		Requests: int(st.UserFacingTotalRequests), PromptTokens: int(st.PromptTokens),
		CompletionTokens: int(st.CompletionTokens), TotalTokens: int(st.TotalTokens), CostUsd: finite(st.TotalCost),
	}
	if out.Requests == 0 {
		return out, nil
	}
	out.ErrorRate = min(max((100-finite(st.UserFacingSuccessRate))/100, 0), 1)
	out.Errors = int(math.Round(float64(out.Requests) * out.ErrorRate))
	if out.LatencyP50Ms, err = percentile(ctx, s.d.Logs, f, 0.5); err != nil {
		return gen.StatsSummary{}, err
	}
	if out.LatencyP95Ms, err = percentile(ctx, s.d.Logs, f, 0.95); err != nil {
		return gen.StatsSummary{}, err
	}
	return out, nil
}

// finite maps NaN and infinities to 0, which JSON cannot carry.
func finite(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return v
}

// group is one member of a group_by set and the filter that narrows to it.
type group struct {
	name   string
	narrow func(*bifrost.LogFilter) bool // false when the member cannot match the base filter
}

// stats answers the stats over f's range: the summary, the series and, with group_by, one summary per member of
// the set that saw requests.
func (s *Server) stats(ctx context.Context, f bifrost.LogFilter, groupBy string) (gen.Stats, error) {
	out := gen.Stats{From: *f.Start, To: *f.End, Series: []gen.StatsPoint{}, Groups: []gen.StatsGroup{}}
	var err error
	if out.Summary, err = s.summary(ctx, f); err != nil {
		return gen.Stats{}, err
	}
	buckets, err := s.d.Logs.Histogram(ctx, f)
	if err != nil {
		return gen.Stats{}, err
	}
	for _, b := range buckets {
		out.Series = append(out.Series, gen.StatsPoint{Time: b.Timestamp, Requests: int(b.Count), Errors: int(b.Error)})
	}
	members, err := s.groups(ctx, groupBy)
	if err != nil {
		return gen.Stats{}, err
	}
	for _, m := range members {
		gf := f
		if !m.narrow(&gf) {
			continue
		}
		sum, err := s.summary(ctx, gf)
		if err != nil {
			return gen.Stats{}, err
		}
		if sum.Requests > 0 {
			out.Groups = append(out.Groups, gen.StatsGroup{Name: m.name, Summary: sum})
		}
	}
	return out, nil
}

// narrowTo sets *field to [id], or keeps it when it already holds id; a base filter on another id cannot match.
func narrowTo(field *[]string, id string) bool {
	if len(*field) > 0 {
		return slices.Contains(*field, id)
	}
	*field = []string{id}
	return true
}

// groups lists the members of a group_by set from the store: client keys by id, aliases and providers by name
// (configured providers, then the subscription providers that have accounts). Members without a front-door id are
// left out.
func (s *Server) groups(ctx context.Context, groupBy string) ([]group, error) {
	var out []group
	switch gen.GetStatsParamsGroupBy(groupBy) {
	case gen.GetStatsParamsGroupByKey:
		if s.d.Keys == nil {
			return nil, errNotImplemented
		}
		keys, err := s.d.Keys.List(ctx)
		if err != nil {
			return nil, err
		}
		for _, k := range keys {
			if vk, ok := s.d.Names.VKByKey(ctx, k.ID); ok {
				out = append(out, group{k.ID, func(f *bifrost.LogFilter) bool { return narrowTo(&f.VirtualKeyIDs, vk) }})
			}
		}
	case gen.GetStatsParamsGroupByAlias:
		if s.d.Aliases == nil {
			return nil, errNotImplemented
		}
		aliases, err := s.d.Aliases.List(ctx)
		if err != nil {
			return nil, err
		}
		for _, a := range aliases {
			if rule, ok := s.d.Names.RuleByAlias(ctx, a.Name); ok {
				out = append(out, group{a.Name, func(f *bifrost.LogFilter) bool { return narrowTo(&f.RoutingRuleIDs, rule) }})
			}
		}
	case gen.GetStatsParamsGroupByProvider:
		names, err := s.providerNames(ctx)
		if err != nil {
			return nil, err
		}
		for _, name := range names {
			out = append(out, group{name, func(f *bifrost.LogFilter) bool {
				ps, ms := s.d.Names.EngineFilter(ctx, name, "")
				if len(f.Providers) > 0 && len(ps) > 0 && !slices.Contains(f.Providers, ps[0]) {
					return false
				}
				f.Providers = ps
				if len(ms) > 0 {
					f.Models = ms
				}
				return true
			}})
		}
	case gen.GetStatsParamsGroupByNone, "":
	default:
		return nil, badRequest("group_by must be none, key, alias or provider")
	}
	return out, nil
}

// providerNames lists the configured providers, then the subscription providers that have accounts.
func (s *Server) providerNames(ctx context.Context) ([]string, error) {
	if s.d.Providers == nil {
		return nil, errNotImplemented
	}
	ps, err := s.d.Providers.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, p := range ps {
		out = append(out, p.Name)
	}
	if s.d.Accounts != nil {
		accts, err := s.d.Accounts.List(ctx, "")
		if err != nil {
			return nil, err
		}
		for _, a := range accts {
			if !slices.Contains(out, a.Provider) {
				out = append(out, a.Provider)
			}
		}
	}
	return out, nil
}

// emptyStats is the answer for a filter that cannot match: zeros, null percentiles, no series and no groups.
func emptyStats(from, to time.Time) gen.Stats {
	return gen.Stats{From: from, To: to, Series: []gen.StatsPoint{}, Groups: []gen.StatsGroup{}}
}

// GetStats answers volume, errors, latency, tokens and cost over a time range (the last 24 hours by default),
// optionally narrowed to a key, an alias or a provider and grouped by one of them.
func (s *Server) GetStats(ctx context.Context, req gen.GetStatsRequestObject) (gen.GetStatsResponseObject, error) {
	if s.d.Logs == nil || s.d.Names == nil {
		return nil, errNotImplemented
	}
	p := req.Params
	start, end, err := s.timeRange(p.From, p.To)
	if err != nil {
		return nil, err
	}
	f := bifrost.LogFilter{Start: &start, End: &end}
	if err := s.scope(ctx, &f, deref(p.Key), deref(p.Alias), deref(p.Provider), ""); errors.Is(err, errNoMatch) {
		return gen.GetStats200JSONResponse(emptyStats(start, end)), nil
	} else if err != nil {
		return nil, err
	}
	out, err := s.stats(ctx, f, string(deref(p.GroupBy)))
	if err != nil {
		return nil, err
	}
	return gen.GetStats200JSONResponse(out), nil
}

// GetKeyUsage answers one client key's stats over a time range. A key the front door has no virtual key for yet
// has seen no requests.
func (s *Server) GetKeyUsage(ctx context.Context, req gen.GetKeyUsageRequestObject) (gen.GetKeyUsageResponseObject, error) {
	if s.d.Logs == nil || s.d.Names == nil || s.d.Keys == nil {
		return nil, errNotImplemented
	}
	if _, err := s.d.Keys.Get(ctx, req.Id); err != nil {
		return nil, err
	}
	start, end, err := s.timeRange(req.Params.From, req.Params.To)
	if err != nil {
		return nil, err
	}
	vk, ok := s.d.Names.VKByKey(ctx, req.Id)
	if !ok {
		return gen.GetKeyUsage200JSONResponse(emptyStats(start, end)), nil
	}
	out, err := s.stats(ctx, bifrost.LogFilter{Start: &start, End: &end, VirtualKeyIDs: []string{vk}}, "")
	if err != nil {
		return nil, err
	}
	return gen.GetKeyUsage200JSONResponse(out), nil
}
