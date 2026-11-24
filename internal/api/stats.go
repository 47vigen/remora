package api

import (
	"context"
	"errors"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/47vigen/remora/internal/api/gen"
	"github.com/47vigen/remora/internal/engine/bifrost"
	"github.com/47vigen/remora/internal/engine/cliproxyapi"
	"github.com/47vigen/remora/internal/stats"
)

// statsBucketSize preserves the log histogram's bucket widths, in seconds.
func statsBucketSize(start, end *time.Time) int64 {
	if start == nil || end == nil {
		return 3600
	}
	const day = 24 * time.Hour
	switch d := end.Sub(*start); {
	case d >= 365*day:
		return 30 * 24 * 3600
	case d >= 90*day:
		return 7 * 24 * 3600
	case d > 31*day:
		return 3 * 24 * 3600
	case d >= 7*day:
		return 24 * 3600
	case d >= 3*day:
		return 8 * 3600
	case d >= day:
		return 3600
	case d >= 2*time.Hour:
		return 600
	default:
		return 60
	}
}

func summary(st stats.Summary) gen.StatsSummary {
	out := gen.StatsSummary{
		Requests: int(st.Requests), Errors: int(st.Errors), PromptTokens: int(st.PromptTokens),
		CompletionTokens: int(st.CompletionTokens), TotalTokens: int(st.TotalTokens), CostUsd: finite(st.CostUSD),
		LatencyP50Ms: st.LatencyP50Ms, LatencyP95Ms: st.LatencyP95Ms,
	}
	if st.Requests > 0 {
		out.ErrorRate = float64(st.Errors) / float64(st.Requests)
	}
	return out
}

// finite maps NaN and infinities to 0, which JSON cannot carry.
func finite(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return v
}

// group is one member of a group_by set and the predicate that narrows to it.
type group struct {
	name   string
	narrow func(stats.Dimensions) bool
	label  *string
}

func (s *Server) stats(ctx context.Context, from, to time.Time, filter func(stats.Dimensions) bool, groupBy string, groupsOnly bool) (gen.Stats, error) {
	out := emptyStats(from, to)
	members, err := s.groups(ctx, from, to, filter, groupBy)
	if err != nil {
		return gen.Stats{}, err
	}
	var bucketSec int64
	if !groupsOnly {
		bucketSec = statsBucketSize(&from, &to)
	} else if len(members) == 0 {
		return out, nil
	}
	predicates := make([]func(stats.Dimensions) bool, len(members))
	for i, m := range members {
		predicates[i] = m.narrow
	}
	res, err := s.d.Stats.Query(ctx, from, to, bucketSec, filter, predicates...)
	if err != nil {
		return gen.Stats{}, err
	}
	if !groupsOnly {
		out.Summary = summary(res.Summary)
		out.Series = series(res.Series)
	}
	for i, m := range members {
		if st := res.Groups[i]; st.Requests > 0 {
			out.Groups = append(out.Groups, gen.StatsGroup{Name: m.name, Label: m.label, Summary: summary(st)})
		}
	}
	return out, nil
}

func series(points []stats.Point) []gen.StatsPoint {
	out := make([]gen.StatsPoint, len(points))
	for i, p := range points {
		out[i] = gen.StatsPoint{
			Time: p.Time, Requests: int(p.Requests), Errors: int(p.Errors), PromptTokens: int(p.PromptTokens),
			CompletionTokens: int(p.CompletionTokens), TotalTokens: int(p.TotalTokens), CostUsd: finite(p.CostUSD),
			LatencyP50Ms: p.LatencyP50Ms, LatencyP95Ms: p.LatencyP95Ms,
		}
	}
	return out
}

func (s *Server) statsProvider(ctx context.Context, name string) func(stats.Dimensions) bool {
	ps, ms := s.d.Names.EngineFilter(ctx, name, "")
	var prefix string
	if isSubscription(name) {
		// Subscription providers share a front-door provider; their models carry the provider name.
		prefix = name + "/"
	}
	return func(d stats.Dimensions) bool {
		return (len(ps) == 0 || slices.Contains(ps, d.Provider)) &&
			(len(ms) == 0 || slices.Contains(ms, d.Model)) && strings.HasPrefix(d.Model, prefix)
	}
}

// groups lists client keys by id and aliases and providers by name. Models are the busiest pairs matching the filter.
func (s *Server) groups(ctx context.Context, from, to time.Time, filter func(stats.Dimensions) bool, groupBy string) ([]group, error) {
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
				out = append(out, group{k.ID, func(d stats.Dimensions) bool { return d.VirtualKeyID == vk }, &k.Name})
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
				out = append(out, group{a.Name, func(d stats.Dimensions) bool { return d.RoutingRuleID == rule }, nil})
			}
		}
	case gen.GetStatsParamsGroupByProvider:
		names, err := s.providerNames(ctx)
		if err != nil {
			return nil, err
		}
		for _, name := range names {
			out = append(out, group{name, s.statsProvider(ctx, name), nil})
		}
	case gen.GetStatsParamsGroupByModel:
		used, err := s.d.Stats.ModelPairs(ctx, from, to, filter)
		if err != nil {
			return nil, err
		}
		for _, u := range used[:min(len(used), 100)] {
			t := s.d.Names.Target(ctx, u.Provider, u.Model)
			out = append(out, group{t.Provider + "/" + t.Model, func(d stats.Dimensions) bool {
				return d.Provider == u.Provider && d.Model == u.Model
			}, nil})
		}
	case gen.GetStatsParamsGroupByNone, "":
	default:
		return nil, badRequest("group_by must be none, key, alias, provider or model")
	}
	return out, nil
}

// providerNames uses the catalog rather than querying live account credentials for historical stats.
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
	for _, p := range cliproxyapi.Providers() {
		if !slices.Contains(out, p.Name) {
			out = append(out, p.Name)
		}
	}
	return out, nil
}

// emptyStats is the answer for a filter that cannot match: zeros, null percentiles, no series and no groups.
func emptyStats(from, to time.Time) gen.Stats {
	return gen.Stats{From: from, To: to, Series: []gen.StatsPoint{}, Groups: []gen.StatsGroup{}}
}

var statsUnavailable = gen.StatsUnavailableJSONResponse{
	Error: gen.ErrorDetail{Type: typeAPI, Code: "stats_unavailable", Message: "Request stats are not available yet."},
}

// GetStats answers volume, errors, latency, tokens and cost over a time range (the last 24 hours by default),
// optionally narrowed to a key, an alias or a provider and grouped by one of them.
func (s *Server) GetStats(ctx context.Context, req gen.GetStatsRequestObject) (gen.GetStatsResponseObject, error) {
	if s.d.Stats == nil {
		return gen.GetStats503JSONResponse{StatsUnavailableJSONResponse: statsUnavailable}, nil
	}
	if s.d.Names == nil {
		return nil, errNotImplemented
	}
	p := req.Params
	start, end, err := s.timeRange(p.From, p.To)
	if err != nil {
		return nil, err
	}
	// Only translate key and alias here: log provider scoping reads the live model catalog.
	var f bifrost.LogFilter
	if err := s.scope(ctx, &f, deref(p.Key), deref(p.Alias), "", ""); errors.Is(err, errNoMatch) {
		return gen.GetStats200JSONResponse(emptyStats(start, end)), nil
	} else if err != nil {
		return nil, err
	}
	provider := s.statsProvider(ctx, deref(p.Provider))
	filter := func(d stats.Dimensions) bool {
		return (len(f.VirtualKeyIDs) == 0 || slices.Contains(f.VirtualKeyIDs, d.VirtualKeyID)) &&
			(len(f.RoutingRuleIDs) == 0 || slices.Contains(f.RoutingRuleIDs, d.RoutingRuleID)) && provider(d)
	}
	out, err := s.stats(ctx, start, end, filter, string(deref(p.GroupBy)), deref(p.GroupsOnly))
	if err != nil {
		return nil, err
	}
	return gen.GetStats200JSONResponse(out), nil
}

// GetKeyUsage answers one client key's stats over a time range. A key the front door has no virtual key for yet
// has seen no requests.
func (s *Server) GetKeyUsage(ctx context.Context, req gen.GetKeyUsageRequestObject) (gen.GetKeyUsageResponseObject, error) {
	if s.d.Stats == nil {
		return gen.GetKeyUsage503JSONResponse{StatsUnavailableJSONResponse: statsUnavailable}, nil
	}
	if s.d.Names == nil || s.d.Keys == nil {
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
	out, err := s.stats(ctx, start, end, func(d stats.Dimensions) bool { return d.VirtualKeyID == vk }, "", false)
	if err != nil {
		return nil, err
	}
	return gen.GetKeyUsage200JSONResponse(out), nil
}
