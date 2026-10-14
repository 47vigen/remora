package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Admin sessions and API tokens are stored only as hashes (sha256 of the value, computed by internal/auth).
// Times are stored as Unix nanoseconds.

// APIToken is an API token for /api/*. Hash is the sha256 of its value; the value itself is never stored.
type APIToken struct {
	ID, Name, Prefix string
	Hash             []byte
	CreatedAt        time.Time
	LastUsedAt       *time.Time
}

// CreateSession stores a session by its hash.
func (s *Store) CreateSession(ctx context.Context, hash []byte, expires time.Time) error {
	_, err := s.db.ExecContext(ctx, "INSERT INTO sessions (hash, expires_at) VALUES (?, ?)", hash, expires.UnixNano())
	if err != nil {
		return fmt.Errorf("store: create session: %w", mapErr(err))
	}
	return nil
}

// SessionExpiry returns when the session with this hash expires. ErrNotFound when there is none.
func (s *Store) SessionExpiry(ctx context.Context, hash []byte) (time.Time, error) {
	var ns int64
	err := s.db.QueryRowContext(ctx, "SELECT expires_at FROM sessions WHERE hash = ?", hash).Scan(&ns)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, ErrNotFound
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("store: session: %w", err)
	}
	return time.Unix(0, ns).UTC(), nil
}

// DeleteSession deletes the session with this hash; deleting an absent session is not an error.
func (s *Store) DeleteSession(ctx context.Context, hash []byte) error {
	if _, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE hash = ?", hash); err != nil {
		return fmt.Errorf("store: delete session: %w", err)
	}
	return nil
}

// DeleteSessionsExcept deletes every session but the one with hash keep (nil keeps none).
func (s *Store) DeleteSessionsExcept(ctx context.Context, keep []byte) error {
	if _, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE hash IS NOT ?", keep); err != nil {
		return fmt.Errorf("store: delete sessions: %w", err)
	}
	return nil
}

// CreateAPIToken stores t. ErrDuplicate when its id or hash exists.
func (s *Store) CreateAPIToken(ctx context.Context, t APIToken) error {
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO api_tokens (id, name, prefix, hash, created_at, last_used_at) VALUES (?, ?, ?, ?, ?, ?)",
		t.ID, t.Name, t.Prefix, t.Hash, t.CreatedAt.UnixNano(), nanos(t.LastUsedAt))
	if err != nil {
		return fmt.Errorf("store: create api token %q: %w", t.Name, mapErr(err))
	}
	return nil
}

const apiTokenCols = "id, name, prefix, hash, created_at, last_used_at" //nolint:gosec // G101: column names, not a credential.

func scanAPIToken(sc interface{ Scan(...any) error }) (APIToken, error) {
	var (
		t        APIToken
		created  int64
		lastUsed sql.NullInt64
	)
	if err := sc.Scan(&t.ID, &t.Name, &t.Prefix, &t.Hash, &created, &lastUsed); err != nil {
		return APIToken{}, err
	}
	t.CreatedAt = time.Unix(0, created).UTC()
	if lastUsed.Valid {
		at := time.Unix(0, lastUsed.Int64).UTC()
		t.LastUsedAt = &at
	}
	return t, nil
}

// ListAPITokens lists the API tokens, oldest first.
func (s *Store) ListAPITokens(ctx context.Context) ([]APIToken, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+apiTokenCols+" FROM api_tokens ORDER BY created_at, id")
	if err != nil {
		return nil, fmt.Errorf("store: api tokens: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []APIToken
	for rows.Next() {
		t, err := scanAPIToken(rows)
		if err != nil {
			return nil, fmt.Errorf("store: api tokens: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: api tokens: %w", err)
	}
	return out, nil
}

// APITokenByHash finds the API token with this hash. ErrNotFound when there is none.
func (s *Store) APITokenByHash(ctx context.Context, hash []byte) (APIToken, error) {
	t, err := scanAPIToken(s.db.QueryRowContext(ctx, "SELECT "+apiTokenCols+" FROM api_tokens WHERE hash = ?", hash))
	if errors.Is(err, sql.ErrNoRows) {
		return APIToken{}, ErrNotFound
	}
	if err != nil {
		return APIToken{}, fmt.Errorf("store: api token by hash: %w", err)
	}
	return t, nil
}

// DeleteAPIToken deletes an API token. ErrNotFound when it does not exist.
func (s *Store) DeleteAPIToken(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM api_tokens WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("store: delete api token %s: %w", id, err)
	}
	return mustAffect(res, "api token "+id)
}

// TouchAPIToken records that the token was used at at. ErrNotFound when it does not exist.
func (s *Store) TouchAPIToken(ctx context.Context, id string, at time.Time) error {
	res, err := s.db.ExecContext(ctx, "UPDATE api_tokens SET last_used_at = ? WHERE id = ?", at.UnixNano(), id)
	if err != nil {
		return fmt.Errorf("store: touch api token %s: %w", id, err)
	}
	return mustAffect(res, "api token "+id)
}

func nanos(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UnixNano()
}
