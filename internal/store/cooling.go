package store

import (
	"context"
	"time"

	"github.com/47vigen/remora/internal/cooling"
)

// LoadCooling deletes expired cooldowns and loads the remaining rows at startup.
func (s *Store) LoadCooling(ctx context.Context, now time.Time) ([]cooling.Cooling, error) {
	var active []cooling.Cooling
	err := s.write(ctx, func(tx *Tx) error {
		if _, err := tx.tx.ExecContext(ctx, `DELETE FROM cooling WHERE until_ns <= ?`, now.UnixNano()); err != nil {
			return err
		}
		rows, err := tx.tx.QueryContext(ctx, `SELECT provider, model, until_ns, reason, backoff_ns FROM cooling`)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var state cooling.Cooling
			var until int64
			if err := rows.Scan(&state.Target.Provider, &state.Target.Model, &until, &state.Reason, &state.Backoff); err != nil {
				return err
			}
			state.Until = time.Unix(0, until).UTC()
			active = append(active, state)
		}
		return rows.Err()
	})
	return active, err
}

// SaveCooling atomically replaces the cooldowns without rebuilding the key and alias snapshot.
func (s *Store) SaveCooling(ctx context.Context, rows []cooling.Cooling) error {
	return s.write(ctx, func(tx *Tx) error {
		if _, err := tx.tx.ExecContext(ctx, `DELETE FROM cooling`); err != nil {
			return err
		}
		for _, state := range rows {
			if _, err := tx.tx.ExecContext(ctx, `INSERT INTO cooling (provider, model, until_ns, reason, backoff_ns)
				VALUES (?, ?, ?, ?, ?)`, state.Target.Provider, state.Target.Model, state.Until.UnixNano(), state.Reason, state.Backoff); err != nil {
				return err
			}
		}
		return nil
	})
}
