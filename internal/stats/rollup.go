package stats

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"time"

	"github.com/47vigen/remora/internal/engine/bifrost"
)

// Dimensions are the untranslated request-log dimensions. Unset IDs are empty.
type Dimensions struct {
	Provider, Model                            string
	VirtualKeyID, RoutingRuleID, SelectedKeyID string
	FallbackIndex                              int
}

const (
	dimensionColumns = `provider, model, vk_id, rule_id, key_id, fallback`
	readLatency      = `SELECT lat FROM minute WHERE (bucket_unix_min, ` + dimensionColumns + `) = (?, ?, ?, ?, ?, ?, ?)`
	upsertMinute     = `INSERT INTO minute VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (bucket_unix_min, ` + dimensionColumns + `) DO UPDATE SET
    requests = requests + excluded.requests, errors = errors + excluded.errors,
    cancelled = cancelled + excluded.cancelled, prompt_tokens = prompt_tokens + excluded.prompt_tokens,
    completion_tokens = completion_tokens + excluded.completion_tokens, total_tokens = total_tokens + excluded.total_tokens,
    cost_usd = cost_usd + excluded.cost_usd, positive_cost_usd = positive_cost_usd + excluded.positive_cost_usd,
    lat = excluded.lat`
)

type minuteKey struct {
	bucket int64
	Dimensions
}

type counts struct {
	requests, errors, cancelled, prompt, completion, tokens int64
	cost, positiveCost                                      float64
}

type tally struct {
	counts
	lat histogram
}

// Ingest records terminal attempts once within the replay window. In-flight
// rows stay eligible when they are delivered again as terminal attempts.
func (s *Store) Ingest(ctx context.Context, rows []bifrost.Log) error {
	if len(rows) == 0 {
		return nil
	}
	return s.ingest(ctx, rows, "", Cursor{})
}

// IngestPage records a page and advances the named cursor in one transaction.
// On restart, resume strictly after pos using timestamp and ID to break ties.
func (s *Store) IngestPage(ctx context.Context, rows []bifrost.Log, name string, pos Cursor) error {
	if name == "" {
		return errors.New("request stats cursor name is empty")
	}
	return s.ingest(ctx, rows, name, pos)
}

func (s *Store) ingest(ctx context.Context, rows []bifrost.Log, cursor string, pos Cursor) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		if err := ingestRows(ctx, tx, rows); err != nil {
			return err
		}
		if cursor != "" {
			_, err := tx.ExecContext(ctx, `INSERT INTO cursor (name, ts, id) VALUES (?, ?, ?)
ON CONFLICT(name) DO UPDATE SET ts = excluded.ts, id = excluded.id`, cursor, pos.Timestamp.UTC().Format(time.RFC3339Nano), pos.ID)
			return err
		}
		return nil
	})
}

func ingestRows(ctx context.Context, tx *sql.Tx, rows []bifrost.Log) error {
	if len(rows) == 0 {
		return nil
	}
	seen, err := tx.PrepareContext(ctx, `INSERT INTO seen (id, ts) VALUES (?, ?) ON CONFLICT(id) DO NOTHING`)
	if err != nil {
		return err
	}
	defer func() { _ = seen.Close() }()
	now := time.Now().Unix()
	minutes := make(map[minuteKey]*tally)
	for _, r := range rows {
		switch r.Status {
		case "success", "error", "cancelled":
		default:
			continue
		}
		if r.ID == "" || r.Timestamp.IsZero() {
			return errors.New("request stats row is missing its ID or timestamp")
		}
		res, err := seen.ExecContext(ctx, r.ID, now)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			continue
		}
		k := minuteKey{unixMinute(r.Timestamp), Dimensions{
			Provider: r.Provider, Model: r.Model, VirtualKeyID: value(r.VirtualKeyID),
			RoutingRuleID: value(r.RoutingRuleID), SelectedKeyID: r.SelectedKeyID, FallbackIndex: r.FallbackIndex,
		}}
		a := minutes[k]
		if a == nil {
			a = &tally{}
			minutes[k] = a
		}
		a.requests++
		switch r.Status {
		case "error":
			a.errors++
		case "cancelled":
			a.cancelled++
		case "success":
			if r.Latency != nil {
				a.lat.observe(*r.Latency)
			}
		}
		if t := r.TokenUsage; t != nil {
			a.prompt += t.PromptTokens
			a.completion += t.CompletionTokens
			a.tokens += t.TotalTokens
		}
		if c := r.Cost; c != nil && !math.IsNaN(*c) && !math.IsInf(*c, 0) {
			a.cost += *c
			a.positiveCost += max(0, *c)
		}
	}
	return flush(ctx, tx, minutes)
}

func flush(ctx context.Context, tx *sql.Tx, minutes map[minuteKey]*tally) error {
	if len(minutes) == 0 {
		return nil
	}
	read, err := tx.PrepareContext(ctx, readLatency)
	if err != nil {
		return err
	}
	defer func() { _ = read.Close() }()
	upsert, err := tx.PrepareContext(ctx, upsertMinute)
	if err != nil {
		return err
	}
	defer func() { _ = upsert.Close() }()
	for k, a := range minutes {
		key := []any{k.bucket, k.Provider, k.Model, k.VirtualKeyID, k.RoutingRuleID, k.SelectedKeyID, k.FallbackIndex}
		var lat []byte
		if err := read.QueryRowContext(ctx, key...).Scan(&lat); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err := a.lat.merge(lat); err != nil {
			return err
		}
		if _, err := upsert.ExecContext(ctx, append(key, a.requests, a.errors, a.cancelled, a.prompt, a.completion,
			a.tokens, a.cost, a.positiveCost, a.lat.encode())...); err != nil {
			return err
		}
	}
	return nil
}

func value(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func unixMinute(t time.Time) int64 { return t.Truncate(time.Minute).Unix() / 60 }
