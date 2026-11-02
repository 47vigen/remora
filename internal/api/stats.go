package api

import (
	"context"
	"errors"
	"math"
	"slices"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/47vigen/remora/internal/api/gen"
	"github.com/47vigen/remora/internal/engine/bifrost"
)

// percentile is the p-quantile (0 < p ≤ 1) of the latency of the n successful attempts f matches, by nearest rank: it
// reads the one row at rank ceil(p·n) from the list sorted by latency. It is nil when n is 0. The front door's log
// store keeps no median, so this is exact rather than read from a histogram. The engine sorts the whole range for it
// and the cost grows with the offset, so the rank is counted from whichever end of the list is nearer.
func percentile(ctx context.Context, src LogSource, f bifrost.LogFilter, p float64, n int64) (*float64, error) {
	if n <= 0 {
		return nil, nil
	}
	rank := min(max(int64(math.Ceil(p*float64(n)-1e-9)), 1), n)
	f.Status, f.Limit, f.SortBy, f.Order, f.Offset = []string{"success"}, 1, "latency", "asc", int(rank-1)
	if rank > n-rank+1 {
		f.Order, f.Offset = "desc", int(n-rank)
	}
	row, err := src.SearchLogs(ctx, f)
	if err != nil {
		return nil, err
	}
	if len(row.Logs) == 0 || row.Logs[0].Latency == nil {
		return nil, nil
	}
	return new(*row.Logs[0].Latency), nil
}

// summary is the stats of what f matches, one row per log row: a fallback attempt counts as a request of its own,
// as in the series and the log list.
func (s *Server) summary(ctx context.Context, f bifrost.LogFilter) (gen.StatsSummary, error) {
	st, err := s.d.Logs.LogStats(ctx, f)
	if err != nil {
		return gen.StatsSummary{}, err
	}
	var done int64
	if st.Completed != nil {
		done = *st.Completed
	}
	out := gen.StatsSummary{
		Requests: int(done), PromptTokens: int(st.PromptTokens), CompletionTokens: int(st.CompletionTokens),
		TotalTokens: int(st.TotalTokens), CostUsd: finite(st.TotalCost),
	}
	if done == 0 {
		return out, nil
	}
	ok := int64(math.Round(float64(done) * min(max(finite(st.SuccessRate), 0), 100) / 100))
	out.Errors = int(done - ok)
	out.ErrorRate = float64(out.Errors) / float64(done)
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() (err error) { out.LatencyP50Ms, err = percentile(gctx, s.d.Logs, f, 0.5, ok); return err })
	g.Go(func() (err error) { out.LatencyP95Ms, err = percentile(gctx, s.d.Logs, f, 0.95, ok); return err })
	if err := g.Wait(); err != nil {
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

// groupReads is how many group summaries are read from the engine at once.
const groupReads = 8

// group is one member of a group_by set and the filter that narrows to it.
type group struct {
	name   string
	narrow func(*bifrost.LogFilter) bool // false when the member cannot match the base filter
}

// stats answers the stats over f's range: the summary, the series and, with group_by, one summary per member of
// the set that saw requests. With groupsOnly the summary and the series are left empty, for a caller that already
// has them.
func (s *Server) stats(ctx context.Context, f bifrost.LogFilter, groupBy string, groupsOnly bool) (gen.Stats, error) {
	out := gen.Stats{From: *f.Start, To: *f.End, Series: []gen.StatsPoint{}, Groups: []gen.StatsGroup{}}
	var err error
	if !groupsOnly {
		if out.Summary, err = s.summary(ctx, f); err != nil {
			return gen.Stats{}, err
		}
		if out.Series, err = s.series(ctx, f); err != nil {
			return gen.Stats{}, err
		}
	}
	members, err := s.groups(ctx, f, groupBy)
	if err != nil {
		return gen.Stats{}, err
	}
	// Each summary is several engine reads, so the members are read side by side.
	sums := make([]gen.StatsSummary, len(members))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(groupReads)
	for i, m := range members {
		gf := f
		if !m.narrow(&gf) {
			continue
		}
		g.Go(func() error {
			var err error
			sums[i], err = s.summary(gctx, gf)
			return err
		})
	}
	if err := g.Wait(); err != nil {
		return gen.Stats{}, err
	}
	for i, m := range members {
		if sums[i].Requests > 0 {
			out.Groups = append(out.Groups, gen.StatsGroup{Name: m.name, Summary: sums[i]})
		}
	}
	return out, nil
}

// series is the time buckets over f's range: the engine's request, token, cost and latency histograms merged by
// bucket start, plus each bucket's median read the way the summary's is. The p95 comes from the engine's latency
// histogram over the successful attempts, the same set the median ranks, whose size the histogram also gives.
func (s *Server) series(ctx context.Context, f bifrost.LogFilter) ([]gen.StatsPoint, error) {
	var (
		counts []bifrost.HistogramBucket
		tokens []bifrost.TokenBucket
		costs  []bifrost.CostBucket
		lat    []bifrost.LatencyBucket
	)
	lf := f
	lf.Status = []string{"success"}
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() (err error) { counts, err = s.d.Logs.Histogram(gctx, f); return err })
	g.Go(func() (err error) { tokens, err = s.d.Logs.TokenHistogram(gctx, f); return err })
	g.Go(func() (err error) { costs, err = s.d.Logs.CostHistogram(gctx, f); return err })
	g.Go(func() (err error) { lat, err = s.d.Logs.LatencyHistogram(gctx, lf); return err })
	if err := g.Wait(); err != nil {
		return nil, err
	}
	out := make([]gen.StatsPoint, len(counts))
	at := make(map[int64]int, len(counts))
	for i, b := range counts {
		at[b.Timestamp.Unix()] = i
		out[i] = gen.StatsPoint{Time: b.Timestamp, Requests: int(b.Count), Errors: int(b.Error)}
	}
	for _, b := range tokens {
		if i, ok := at[b.Timestamp.Unix()]; ok {
			out[i].PromptTokens, out[i].CompletionTokens = int(b.PromptTokens), int(b.CompletionTokens)
			out[i].TotalTokens = int(b.TotalTokens)
		}
	}
	for _, b := range costs {
		if i, ok := at[b.Timestamp.Unix()]; ok {
			out[i].CostUsd = finite(b.TotalCost)
		}
	}
	ranked := make([]int64, len(counts)) // successful attempts with a latency, per bucket
	for _, b := range lat {
		if i, ok := at[b.Timestamp.Unix()]; ok && b.TotalRequests > 0 {
			out[i].LatencyP95Ms = new(finite(b.P95Latency))
			ranked[i] = b.TotalRequests
		}
	}
	// The engine has no median histogram, so each bucket that saw a success ranks its own window.
	g, gctx = errgroup.WithContext(ctx)
	g.SetLimit(groupReads)
	for i := range counts {
		if ranked[i] == 0 {
			continue
		}
		start, end := *f.Start, *f.End
		if ts := counts[i].Timestamp; ts.After(start) {
			start = ts
		}
		if i+1 < len(counts) && counts[i+1].Timestamp.Before(end) {
			end = counts[i+1].Timestamp.Add(-time.Nanosecond)
		}
		bf := f
		bf.Start, bf.End = &start, &end
		g.Go(func() (err error) {
			out[i].LatencyP50Ms, err = percentile(gctx, s.d.Logs, bf, 0.5, ranked[i])
			return err
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
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
// left out. Models come from the log instead: the ones that served what f matches, named "<provider>/<model>".
func (s *Server) groups(ctx context.Context, f bifrost.LogFilter, groupBy string) ([]group, error) {
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
			var ms []string
			if isSubscription(name) {
				if ms, err = s.subscriptionModels(ctx, name); err != nil {
					return nil, err
				}
				if len(ms) == 0 {
					continue
				}
			}
			out = append(out, group{name, func(f *bifrost.LogFilter) bool {
				ps, _ := s.d.Names.EngineFilter(ctx, name, "")
				if len(f.Providers) > 0 && len(ps) > 0 && !slices.Contains(f.Providers, ps[0]) {
					return false
				}
				f.Providers = ps
				if len(ms) > 0 {
					if len(f.Models) > 0 {
						ms = slices.DeleteFunc(slices.Clone(ms), func(m string) bool { return !slices.Contains(f.Models, m) })
						if len(ms) == 0 {
							return false
						}
					}
					f.Models = ms
				}
				return true
			}})
		}
	case gen.GetStatsParamsGroupByModel:
		used, err := s.d.Logs.ModelUsage(ctx, f)
		if err != nil {
			return nil, err
		}
		for _, u := range used {
			t := s.d.Names.Target(ctx, u.Provider, u.Model)
			// The pair was read under the base filter, so it always matches it.
			out = append(out, group{t.Provider + "/" + t.Model, func(f *bifrost.LogFilter) bool {
				f.Providers, f.Models = []string{u.Provider}, []string{u.Model}
				return true
			}})
		}
	case gen.GetStatsParamsGroupByNone, "":
	default:
		return nil, badRequest("group_by must be none, key, alias, provider or model")
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
	out, err := s.stats(ctx, f, string(deref(p.GroupBy)), deref(p.GroupsOnly))
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
	out, err := s.stats(ctx, bifrost.LogFilter{Start: &start, End: &end, VirtualKeyIDs: []string{vk}}, "", false)
	if err != nil {
		return nil, err
	}
	return gen.GetKeyUsage200JSONResponse(out), nil
}
