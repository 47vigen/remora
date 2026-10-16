package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// QuotaWindow is one quota window of an account. Scope is a model family or model ("gemini", "claude-gpt", "opus",
// "" = the whole account); Window is "5h", "weekly" or the length in minutes. Remaining is 0..1, nil = unknown;
// ResetsAt nil = unknown or no reset.
type QuotaWindow struct {
	Scope     string     `json:"scope"`
	Window    string     `json:"window"`
	Remaining *float64   `json:"remaining"`
	ResetsAt  *time.Time `json:"resets_at"`
}

// AccountQuota is an account's last quota snapshot. FetchedAt is when the windows were observed (zero = never);
// CheckedAt is the last poll attempt, successful or not.
type AccountQuota struct {
	AccountID    int64
	Plan         string
	Windows      []QuotaWindow
	LimitReached bool
	Source       string // "poll" | "passive"
	FetchedAt    time.Time
	CheckedAt    time.Time
	Error        string
}

func formatQuotaTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(accountTime)
}

func parseQuotaTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	return time.Parse(accountTime, s)
}

// PutAccountQuota stores q as its account's snapshot, replacing the previous one. ErrNotFound when the account does
// not exist.
func (s *Store) PutAccountQuota(ctx context.Context, q AccountQuota) error {
	windows := q.Windows
	if windows == nil {
		windows = []QuotaWindow{}
	}
	raw, err := json.Marshal(windows)
	if err != nil {
		return fmt.Errorf("store: account %d quota: %w", q.AccountID, err)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO account_quota
		(account_id, plan, windows, limit_reached, source, fetched_at, checked_at, error) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (account_id) DO UPDATE SET plan = excluded.plan, windows = excluded.windows,
		limit_reached = excluded.limit_reached, source = excluded.source, fetched_at = excluded.fetched_at,
		checked_at = excluded.checked_at, error = excluded.error`,
		q.AccountID, q.Plan, string(raw), boolInt(q.LimitReached), q.Source,
		formatQuotaTime(q.FetchedAt), formatQuotaTime(q.CheckedAt), q.Error)
	var se *sqlite.Error
	if errors.As(err, &se) && se.Code() == sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY {
		return fmt.Errorf("%w: account %s", ErrNotFound, strconv.FormatInt(q.AccountID, 10))
	}
	if err != nil {
		return fmt.Errorf("store: account %d quota: %w", q.AccountID, err)
	}
	return nil
}

// AccountQuotas returns every stored snapshot by account id.
func (s *Store) AccountQuotas(ctx context.Context) (map[int64]AccountQuota, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT account_id, plan, windows, limit_reached, source, fetched_at, checked_at, error FROM account_quota")
	if err != nil {
		return nil, fmt.Errorf("store: account quotas: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[int64]AccountQuota{}
	for rows.Next() {
		var (
			q                         AccountQuota
			windows, fetched, checked string
		)
		if err := rows.Scan(&q.AccountID, &q.Plan, &windows, &q.LimitReached, &q.Source, &fetched, &checked, &q.Error); err != nil {
			return nil, fmt.Errorf("store: account quotas: %w", err)
		}
		if err := json.Unmarshal([]byte(windows), &q.Windows); err != nil {
			return nil, fmt.Errorf("store: account %d quota windows: %w", q.AccountID, err)
		}
		if q.FetchedAt, err = parseQuotaTime(fetched); err != nil {
			return nil, fmt.Errorf("store: account %d quota fetched_at: %w", q.AccountID, err)
		}
		if q.CheckedAt, err = parseQuotaTime(checked); err != nil {
			return nil, fmt.Errorf("store: account %d quota checked_at: %w", q.AccountID, err)
		}
		out[q.AccountID] = q
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: account quotas: %w", err)
	}
	return out, nil
}
