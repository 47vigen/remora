package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// EngineValue returns the value stored for engine under key. ok is false when it is absent.
func (s *Store) EngineValue(ctx context.Context, engine, key string) (value string, ok bool, err error) {
	err = s.db.QueryRowContext(ctx,
		"SELECT value FROM engine_state WHERE engine = ? AND key = ?", engine, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("store: engine value %s/%s: %w", engine, key, err)
	}
	return value, true, nil
}

// SetEngineValue stores value for engine under key, replacing any previous value.
func (s *Store) SetEngineValue(ctx context.Context, engine, key, value string) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO engine_state (engine, key, value) VALUES (?, ?, ?)
ON CONFLICT (engine, key) DO UPDATE SET
  value = excluded.value,
  updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now')`, engine, key, value)
	if err != nil {
		return fmt.Errorf("store: set engine value %s/%s: %w", engine, key, err)
	}
	return nil
}
