package stats

import (
	"math"
	"math/rand/v2"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/47vigen/remora/internal/engine/bifrost"
)

var epoch = time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)

func openTestStore(t testing.TB, path string) *Store {
	t.Helper()
	s, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { must(t, s.Close()) })
	return s
}

func must(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestRollupMatchesRaw(t *testing.T) {
	s := openTestStore(t, filepath.Join(t.TempDir(), "stats.db"))
	from, to := epoch, epoch.Add(59*time.Minute+59*time.Second)
	rng := rand.New(rand.NewPCG(3, 5)) // #nosec G404 -- deterministic test data.
	rows := make([]bifrost.Log, 1000)
	for i := range rows {
		rows[i] = bifrost.Log{
			ID: strconv.Itoa(i), Timestamp: epoch.Add(time.Duration(rng.IntN(5400)-900) * time.Second),
			Provider: []string{"a", "b"}[rng.IntN(2)], Model: []string{"only-a", "shared", ""}[rng.IntN(3)],
			VirtualKeyID: new([]string{"v1", "v2"}[rng.IntN(2)]), RoutingRuleID: new("r"), SelectedKeyID: "k",
			FallbackIndex: rng.IntN(3), ParentRequestID: new("root"),
			Status:     []string{"success", "success", "error", "cancelled", "processing", "unknown"}[rng.IntN(6)],
			Cost:       new(float64(rng.IntN(15)-4) / 1024),
			TokenUsage: &bifrost.TokenUsage{PromptTokens: int64(rng.IntN(50)), CompletionTokens: 5, TotalTokens: 7},
		}
		if rng.IntN(8) != 0 {
			rows[i].Latency = new(10 * math.Pow(1.2, rng.Float64()*52))
		}
	}
	for batch := range slices.Chunk(rows, 200) {
		must(t, s.Ingest(t.Context(), batch))
	}

	filter := func(d Dimensions) bool { return d.VirtualKeyID == "v1" && d.RoutingRuleID == "r" }
	result, err := s.Query(t.Context(), from, to, 600, filter,
		func(d Dimensions) bool { return d.Provider == "a" },
		func(d Dimensions) bool { return d.Model == "shared" && d.SelectedKeyID == "k" && d.FallbackIndex != 0 })
	must(t, err)
	base := func(r bifrost.Log) bool { return *r.VirtualKeyID == "v1" }
	compareRaw(t, result.Summary, rows, from, to, base, false)
	compareRaw(t, result.Groups[0], rows, from, to, func(r bifrost.Log) bool { return base(r) && r.Provider == "a" }, false)
	compareRaw(t, result.Groups[1], rows, from, to, func(r bifrost.Log) bool {
		return base(r) && r.Model == "shared" && r.SelectedKeyID == "k" && r.FallbackIndex != 0
	}, false)
	if len(result.Series) != 6 {
		t.Fatalf("series buckets = %d, want 6", len(result.Series))
	}
	for i, point := range result.Series {
		start := from.Add(time.Duration(i) * 10 * time.Minute)
		if !point.Time.Equal(start) {
			t.Errorf("bucket time = %v, want %v", point.Time, start)
		}
		compareRaw(t, point.Summary, rows, start, minTime(start.Add(10*time.Minute-time.Nanosecond), to), base, true)
	}
	pairs, err := s.ModelPairs(t.Context(), from, to, filter)
	must(t, err)
	for _, p := range []ModelPair{{"a", "only-a"}, {"b", "only-a"}, {"a", "shared"}, {"b", "shared"}} {
		if !slices.Contains(pairs, p) {
			t.Errorf("missing pair %v in %v", p, pairs)
		}
	}
	if len(pairs) != 4 {
		t.Errorf("unexpected pairs: %v", pairs)
	}
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func compareRaw(t *testing.T, got Summary, rows []bifrost.Log, from, to time.Time, filter func(bifrost.Log) bool, series bool) {
	t.Helper()
	var want Summary
	var success int
	var latency []float64
	for _, r := range rows {
		if r.Timestamp.Before(from) || r.Timestamp.After(to) || !filter(r) {
			continue
		}
		switch r.Status {
		case "success":
			success++
			if r.Latency != nil {
				latency = append(latency, *r.Latency)
			}
		case "error":
			want.Errors++
		case "cancelled":
			if !series {
				want.Errors++
			}
		default:
			continue
		}
		want.Requests++
		want.PromptTokens += r.TokenUsage.PromptTokens
		want.CompletionTokens += r.TokenUsage.CompletionTokens
		want.TotalTokens += r.TokenUsage.TotalTokens
		if !series || *r.Cost > 0 {
			want.CostUSD += *r.Cost
		}
	}
	slices.Sort(latency)
	for _, q := range []struct {
		p   float64
		got *float64
	}{{.5, got.LatencyP50Ms}, {.95, got.LatencyP95Ms}} {
		rank := int(math.Ceil(q.p * float64(success)))
		if rank > success-rank+1 {
			rank = len(latency) - (success - rank)
		}
		if rank <= 0 || rank > len(latency) {
			if q.got != nil {
				t.Errorf("p%v = %v, want nil", q.p*100, *q.got)
			}
		} else if exact := latency[rank-1]; q.got == nil || math.Abs(*q.got/exact-1) > .1 {
			t.Errorf("p%v = %v, want within 10%% of %v", q.p*100, q.got, exact)
		}
	}
	got.LatencyP50Ms, got.LatencyP95Ms = nil, nil
	if got != want {
		t.Fatalf("rollup = %+v, raw = %+v", got, want)
	}
}

func TestRollupLatencyEncoding(t *testing.T) {
	s := openTestStore(t, filepath.Join(t.TempDir(), "stats.db"))
	rows := make([]bifrost.Log, 201)
	for i := range rows {
		rows[i] = bifrost.Log{
			ID: strconv.Itoa(i), Timestamp: epoch.Add(time.Duration(i/200) * time.Minute),
			Status: "success", Latency: new(10.0),
		}
	}
	rows[200].Latency = new(99.0)
	must(t, s.Ingest(t.Context(), rows))
	res, err := s.Query(t.Context(), epoch, epoch.Add(time.Minute), 120, nil)
	must(t, err)
	if res.Summary.LatencyP95Ms == nil || *res.Summary.LatencyP95Ms > 11 || *res.Summary.LatencyP95Ms < 10 {
		t.Fatalf("lost the multi-byte count in bin zero: %+v", res.Summary)
	}
}

func TestRollupExactlyOnce(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "stats.db")
	s := openTestStore(t, path)
	now := time.Now().UTC()
	stamp := now.AddDate(0, 0, -7).Truncate(time.Minute)
	rows := []bifrost.Log{
		{ID: "root", Timestamp: stamp, Status: "processing"},
		{ID: "fallback", ParentRequestID: new("root"), FallbackIndex: 1, Timestamp: stamp, Status: "success"},
	}
	must(t, s.Ingest(ctx, rows))
	rows[0].Status = "error"
	pos := Cursor{Timestamp: stamp.Add(time.Nanosecond), ID: "fallback"}
	must(t, s.IngestPage(ctx, rows, "backfill", pos))
	must(t, s.Close())
	s = openTestStore(t, path)
	got, err := s.Cursor(ctx, "backfill")
	must(t, err)
	if got != pos {
		t.Errorf("cursor = %+v, want %+v", got, pos)
	}
	// A historical page just seen by backfill is still protected when pruning runs.
	must(t, s.Prune(ctx, now, 7))
	must(t, s.Ingest(ctx, rows))
	res, err := s.Query(ctx, stamp, stamp, 60, nil)
	must(t, err)
	if res.Summary != (Summary{Requests: 2, Errors: 1}) {
		t.Fatalf("summary after reopen/replay = %+v", res.Summary)
	}

	_, err = s.db.ExecContext(ctx, `CREATE TRIGGER reject BEFORE UPDATE ON minute BEGIN SELECT RAISE(ABORT, 'reject'); END`)
	must(t, err)
	rows = []bifrost.Log{{ID: "new", Timestamp: stamp, Status: "error"}}
	if err := s.IngestPage(ctx, rows, "backfill", Cursor{Timestamp: stamp, ID: "new"}); err == nil {
		t.Fatal("expected ingest to fail")
	}
	got, err = s.Cursor(ctx, "backfill")
	must(t, err)
	if got != pos {
		t.Errorf("cursor advanced on failed ingest: %+v", got)
	}
	_, err = s.db.ExecContext(ctx, "DROP TRIGGER reject")
	must(t, err)
	must(t, s.Ingest(ctx, rows))
	res, err = s.Query(ctx, stamp, stamp, 0, nil)
	must(t, err)
	if res.Summary != (Summary{Requests: 3, Errors: 2}) {
		t.Fatalf("retry after rollback = %+v", res.Summary)
	}
}

func TestRollupPrune(t *testing.T) {
	ctx := t.Context()
	s := openTestStore(t, filepath.Join(t.TempDir(), "stats.db"))
	const day = 24 * time.Hour
	old := epoch.Add(-11 * day)
	rows := []bifrost.Log{
		{ID: "expired", Timestamp: old, Status: "success"},
		{ID: "extended", Timestamp: epoch.Add(-10 * day), Status: "success"},
		{ID: "floor", Timestamp: epoch.Add(-8 * day), Status: "success"},
	}
	must(t, s.Ingest(ctx, rows))
	_, err := s.db.ExecContext(ctx, "UPDATE seen SET ts = CASE id WHEN 'expired' THEN ? ELSE ? END",
		epoch.Add(-16*time.Minute).Unix(), epoch.Add(-14*time.Minute).Unix())
	must(t, err)
	must(t, s.Prune(ctx, epoch, 9))
	res, err := s.Query(ctx, old, epoch, 0, nil)
	must(t, err)
	if res.Summary.Requests != 2 {
		t.Fatalf("extended retention kept %d requests, want 2", res.Summary.Requests)
	}
	must(t, s.Prune(ctx, epoch, 3))
	res, err = s.Query(ctx, old, epoch, 0, nil)
	must(t, err)
	if res.Summary.Requests != 1 {
		t.Fatalf("minimum retention kept %d requests, want 1", res.Summary.Requests)
	}
	var seen int
	must(t, s.db.QueryRowContext(ctx, "SELECT count(*) FROM seen").Scan(&seen))
	if seen != 2 {
		t.Errorf("seen count = %d, want 2", seen)
	}
}

func TestRollupSchemaRebuild(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "stats ?.db")
	s := openTestStore(t, path)
	rows := []bifrost.Log{{ID: "row", Timestamp: epoch, Status: "success"}}
	must(t, s.IngestPage(ctx, rows, "backfill", Cursor{Timestamp: epoch, ID: "row"}))
	_, err := s.db.ExecContext(ctx, "CREATE TABLE obsolete (foo); PRAGMA user_version = 42")
	must(t, err)
	must(t, s.Close())
	s = openTestStore(t, path)
	var version, obsolete, minutes, seen, busyTimeout int
	var journal string
	must(t, s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version))
	must(t, s.db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE name = 'obsolete'").Scan(&obsolete))
	must(t, s.db.QueryRowContext(ctx, "SELECT count(*) FROM minute").Scan(&minutes))
	must(t, s.db.QueryRowContext(ctx, "SELECT count(*) FROM seen").Scan(&seen))
	if version != schemaVersion || obsolete != 0 || minutes != 0 || seen != 0 {
		t.Fatalf("rebuild schema: version=%d obsolete=%d minutes=%d seen=%d", version, obsolete, minutes, seen)
	}
	must(t, s.db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journal))
	must(t, s.db.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busyTimeout))
	if journal != "wal" || busyTimeout <= 0 || s.db.Stats().MaxOpenConnections != 1 {
		t.Fatalf("expected WAL, busy timeout and one connection; got %s, %dms, %d", journal, busyTimeout, s.db.Stats().MaxOpenConnections)
	}
	pos, err := s.Cursor(ctx, "backfill")
	must(t, err)
	if pos != (Cursor{}) {
		t.Fatalf("rebuild kept cursor %+v", pos)
	}
	must(t, s.Ingest(ctx, rows))
	res, err := s.Query(ctx, epoch, epoch, 60, nil)
	must(t, err)
	if res.Summary.Requests != 1 {
		t.Fatalf("backfill after rebuild: %+v", res.Summary)
	}
}

func BenchmarkRollupQuery7d(b *testing.B) {
	s := openTestStore(b, filepath.Join(b.TempDir(), "stats.db"))
	const days, minutesPerDay, rowsPerMinute = 7, 24 * 60, 35
	providers, models, keys := []string{"a", "b", "c"}, []string{"m1", "m2", "m3"}, []string{"v1", "v2"}
	usage := &bifrost.TokenUsage{PromptTokens: 20, CompletionTokens: 10, TotalTokens: 30}
	rows := make([]bifrost.Log, minutesPerDay*rowsPerMinute)
	for day := range days {
		for i := range rows {
			d := i % 6
			rows[i] = bifrost.Log{
				ID: strconv.Itoa(day*len(rows) + i), Timestamp: epoch.Add(time.Duration(day*minutesPerDay+i/rowsPerMinute) * time.Minute),
				Provider: providers[d/2], Model: models[d%3], VirtualKeyID: &keys[d%2], SelectedKeyID: keys[d%2],
				Status: "success", Latency: new(float64(100 + (i%35)*40)), TokenUsage: usage, Cost: new(.002),
			}
		}
		must(b, s.Ingest(b.Context(), rows))
	}
	to := epoch.Add(days*24*time.Hour - time.Nanosecond)
	b.ReportAllocs()
	for b.Loop() {
		res, err := s.Query(b.Context(), epoch, to, 86400, nil,
			func(d Dimensions) bool { return d.Provider == "a" },
			func(d Dimensions) bool { return d.Provider == "b" },
			func(d Dimensions) bool { return d.Provider == "c" })
		must(b, err)
		if res.Summary.Requests != days*minutesPerDay*rowsPerMinute || len(res.Series) != days || res.Summary.LatencyP95Ms == nil {
			b.Fatal("incomplete result")
		}
	}
}
