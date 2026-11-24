package stats

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"slices"
	"time"
)

// Summary holds attempt totals and approximate percentiles of successful attempts.
type Summary struct {
	Requests, Errors                            int64
	PromptTokens, CompletionTokens, TotalTokens int64
	CostUSD                                     float64
	LatencyP50Ms, LatencyP95Ms                  *float64
}

// Point is one UTC series bucket. Unlike Summary, its Errors exclude cancelled
// attempts and its CostUSD only includes positive charges, like the log histograms.
type Point struct {
	Time time.Time
	Summary
}

// Result contains the totals, the zero-filled series, and one summary per group predicate in input order.
type Result struct {
	Summary Summary
	Series  []Point
	Groups  []Summary
}

// Grouping by one packed key keeps SQLite from comparing six columns per sorter step.
const dimensionGroup = `json_array(` + dimensionColumns + `)`

const queryTotals = `SELECT ` + dimensionColumns + `,
    SUM(requests), SUM(errors), SUM(cancelled), SUM(prompt_tokens), SUM(completion_tokens), SUM(total_tokens),
    SUM(cost_usd), SUM(positive_cost_usd), CAST(group_concat(lat, '') AS BLOB)
FROM minute WHERE bucket_unix_min BETWEEN ? AND ? GROUP BY ` + dimensionGroup

// Query includes both boundary minutes; a minute cannot be split. bucketSec must
// be a positive multiple of 60, or zero for no series. Predicates are evaluated
// while reading the store and must not call it; nil matches all dimensions.
func (s *Store) Query(ctx context.Context, from, to time.Time, bucketSec int64, filter func(Dimensions) bool, groups ...func(Dimensions) bool) (Result, error) {
	if from.After(to) || bucketSec < 0 || bucketSec%60 != 0 {
		return Result{}, errors.New("invalid request stats range or bucket width")
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = conn.Close() }()
	stmt, err := conn.PrepareContext(ctx, queryTotals)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = stmt.Close() }()
	lo, hi := unixMinute(from), unixMinute(to)
	start, width := lo, hi-lo+1
	if bucketSec > 0 {
		width = bucketSec / 60
		start = lo - (lo%width+width)%width
	}
	var total tally
	grouped := make([]tally, len(groups))
	out := Result{Series: []Point{}, Groups: make([]Summary, len(groups))}
	for minute := start; minute <= hi; minute += width {
		rows, err := stmt.QueryContext(ctx, max(lo, minute), min(hi, minute+width-1))
		if err != nil {
			return Result{}, err
		}
		point, err := readBucket(rows, filter, groups, grouped)
		if err != nil {
			return Result{}, err
		}
		total.add(&point)
		if bucketSec > 0 {
			out.Series = append(out.Series, Point{Time: time.Unix(minute*60, 0).UTC(), Summary: point.summary(true)})
		}
	}
	out.Summary = total.summary(false)
	for i := range grouped {
		out.Groups[i] = grouped[i].summary(false)
	}
	return out, nil
}

func readBucket(rows *sql.Rows, filter func(Dimensions) bool, groups []func(Dimensions) bool, grouped []tally) (tally, error) {
	defer func() { _ = rows.Close() }()
	var total tally
	for rows.Next() {
		var d Dimensions
		var a tally
		var b []byte
		if err := rows.Scan(&d.Provider, &d.Model, &d.VirtualKeyID, &d.RoutingRuleID, &d.SelectedKeyID,
			&d.FallbackIndex, &a.requests, &a.errors, &a.cancelled, &a.prompt, &a.completion, &a.tokens,
			&a.cost, &a.positiveCost, &b); err != nil {
			return tally{}, err
		}
		if filter != nil && !filter(d) {
			continue
		}
		if err := a.lat.merge(b); err != nil {
			return tally{}, err
		}
		total.add(&a)
		for i, match := range groups {
			if match == nil || match(d) {
				grouped[i].add(&a)
			}
		}
	}
	return total, rows.Err()
}

func (a *tally) add(b *tally) {
	a.requests += b.requests
	a.errors += b.errors
	a.cancelled += b.cancelled
	a.prompt += b.prompt
	a.completion += b.completion
	a.tokens += b.tokens
	a.cost += b.cost
	a.positiveCost += b.positiveCost
	for i, n := range b.lat {
		a.lat[i] += n
	}
}

func (a *tally) summary(series bool) Summary {
	successes := a.requests - a.errors - a.cancelled
	out := Summary{
		Requests: a.requests, Errors: a.errors + a.cancelled, CostUSD: a.cost,
		PromptTokens: a.prompt, CompletionTokens: a.completion, TotalTokens: a.tokens,
		LatencyP50Ms: a.lat.percentile(.5, successes), LatencyP95Ms: a.lat.percentile(.95, successes),
	}
	if series {
		out.Errors, out.CostUSD = a.errors, a.positiveCost
	}
	return out
}

// ModelPair identifies an untranslated provider and model.
type ModelPair struct{ Provider, Model string }

// ModelPairs returns distinct nonempty models that match the range and filter,
// busiest first, breaking ties by model and then provider as in the log rankings.
// The predicate has the same restrictions as in Query.
func (s *Store) ModelPairs(ctx context.Context, from, to time.Time, filter func(Dimensions) bool) ([]ModelPair, error) {
	if from.After(to) {
		return nil, errors.New("invalid request stats range")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+dimensionColumns+`, SUM(requests) FROM minute
WHERE bucket_unix_min BETWEEN ? AND ? AND model != '' GROUP BY `+dimensionGroup, unixMinute(from), unixMinute(to))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	counts := make(map[ModelPair]int64)
	for rows.Next() {
		var d Dimensions
		var n int64
		if err := rows.Scan(&d.Provider, &d.Model, &d.VirtualKeyID, &d.RoutingRuleID, &d.SelectedKeyID, &d.FallbackIndex, &n); err != nil {
			return nil, err
		}
		if filter == nil || filter(d) {
			counts[ModelPair{d.Provider, d.Model}] += n
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	pairs := make([]ModelPair, 0, len(counts))
	for p := range counts {
		pairs = append(pairs, p)
	}
	slices.SortFunc(pairs, func(a, b ModelPair) int {
		return cmp.Or(cmp.Compare(counts[b], counts[a]), cmp.Compare(a.Model, b.Model), cmp.Compare(a.Provider, b.Provider))
	})
	return pairs, nil
}
