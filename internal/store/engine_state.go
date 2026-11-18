package store

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

const engineValueVersion = "v1:"

func engineValueAAD(engine, key string) string { return "engine_state/" + engine + "/" + key }

func (sl *Sealer) sealEngineValue(engine, key, value string) (string, error) {
	sealed, err := sl.Seal(value, engineValueAAD(engine, key))
	if err != nil {
		return "", err
	}
	return engineValueVersion + base64.RawStdEncoding.EncodeToString(sealed), nil
}

// openEngineValue opens a sealed value or returns legacy plaintext without rewriting it.
func (sl *Sealer) openEngineValue(engine, key, value string) (string, error) {
	encoded, sealed := strings.CutPrefix(value, engineValueVersion)
	if !sealed {
		return value, nil
	}
	ciphertext, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("store: decode engine value %s/%s: %w", engine, key, err)
	}
	return sl.Open(ciphertext, engineValueAAD(engine, key))
}

// EngineValue returns the value stored for engine under key, sealing legacy plaintext on first read.
// ok is false when the value is absent.
func (s *Store) EngineValue(ctx context.Context, engine, key string) (value string, ok bool, err error) {
	err = s.db.QueryRowContext(ctx,
		"SELECT value FROM engine_state WHERE engine = ? AND key = ?", engine, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("store: engine value %s/%s: %w", engine, key, err)
	}
	sl := s.sealer.Load()
	if sl == nil {
		return "", false, ErrNoSealer
	}
	if strings.HasPrefix(value, engineValueVersion) {
		plaintext, err := sl.openEngineValue(engine, key, value)
		return plaintext, err == nil, err
	}
	sealed, err := sl.sealEngineValue(engine, key, value)
	if err != nil {
		return "", false, err
	}
	// Do not overwrite a value another writer changed since the SELECT.
	if _, err := s.db.ExecContext(ctx,
		"UPDATE engine_state SET value = ? WHERE engine = ? AND key = ? AND value = ?", sealed, engine, key, value); err != nil {
		return "", false, fmt.Errorf("store: seal legacy engine value %s/%s: %w", engine, key, err)
	}
	return value, true, nil
}

// SetEngineValue seals and stores value for engine under key, replacing any previous value.
func (s *Store) SetEngineValue(ctx context.Context, engine, key, value string) error {
	sl := s.sealer.Load()
	if sl == nil {
		return ErrNoSealer
	}
	sealed, err := sl.sealEngineValue(engine, key, value)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO engine_state (engine, key, value) VALUES (?, ?, ?)
ON CONFLICT (engine, key) DO UPDATE SET
  value = excluded.value,
  updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now')`, engine, key, sealed)
	if err != nil {
		return fmt.Errorf("store: set engine value %s/%s: %w", engine, key, err)
	}
	return nil
}
