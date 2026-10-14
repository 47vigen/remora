package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// Account is a subscription account: one OAuth engine credential remora adopted. Provider is the user-facing
// provider name ("grok", not the engine's "xai"); Credential is the engine's credential file name.
type Account struct {
	ID                          int64
	Provider, Label, Credential string
	Enabled                     bool
	CreatedAt                   time.Time
}

// accountTime is how created_at is stored: UTC with milliseconds, like the other tables' timestamps.
const accountTime = "2006-01-02T15:04:05.000Z"

const accountCols = "id, provider, label, credential, enabled, created_at"

func scanAccount(sc interface{ Scan(...any) error }) (Account, error) {
	var (
		a       Account
		created string
	)
	if err := sc.Scan(&a.ID, &a.Provider, &a.Label, &a.Credential, &a.Enabled, &created); err != nil {
		return Account{}, err
	}
	t, err := time.Parse(accountTime, created)
	if err != nil {
		return Account{}, fmt.Errorf("account %d: created_at: %w", a.ID, err)
	}
	a.CreatedAt = t
	return a, nil
}

// CreateAccount stores a new account and returns it with ID and CreatedAt set (a.ID and a.CreatedAt are ignored).
// ErrDuplicate when another account already has a.Credential.
func (s *Store) CreateAccount(ctx context.Context, a Account) (Account, error) {
	a.CreatedAt = time.Now().UTC().Truncate(time.Millisecond)
	res, err := s.db.ExecContext(ctx,
		"INSERT INTO accounts (provider, label, credential, enabled, created_at) VALUES (?, ?, ?, ?, ?)",
		a.Provider, a.Label, a.Credential, boolInt(a.Enabled), a.CreatedAt.Format(accountTime))
	if err != nil {
		return Account{}, fmt.Errorf("store: create account %q: %w", a.Credential, mapErr(err))
	}
	if a.ID, err = res.LastInsertId(); err != nil {
		return Account{}, fmt.Errorf("store: create account %q: %w", a.Credential, err)
	}
	return a, nil
}

// Accounts lists the accounts ordered by provider, then id.
func (s *Store) Accounts(ctx context.Context) ([]Account, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+accountCols+" FROM accounts ORDER BY provider, id")
	if err != nil {
		return nil, fmt.Errorf("store: accounts: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Account
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, fmt.Errorf("store: accounts: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: accounts: %w", err)
	}
	return out, nil
}

// Account returns one account. ErrNotFound when it does not exist.
func (s *Store) Account(ctx context.Context, id int64) (Account, error) {
	a, err := scanAccount(s.db.QueryRowContext(ctx, "SELECT "+accountCols+" FROM accounts WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, fmt.Errorf("%w: account %d", ErrNotFound, id)
	}
	if err != nil {
		return Account{}, fmt.Errorf("store: account %d: %w", id, err)
	}
	return a, nil
}

// SetAccountEnabled enables or disables an account. ErrNotFound when it does not exist.
func (s *Store) SetAccountEnabled(ctx context.Context, id int64, enabled bool) error {
	res, err := s.db.ExecContext(ctx, "UPDATE accounts SET enabled = ? WHERE id = ?", boolInt(enabled), id)
	if err != nil {
		return fmt.Errorf("store: set account %d enabled: %w", id, err)
	}
	return mustAffect(res, "account "+strconv.FormatInt(id, 10))
}

// DeleteAccount deletes an account. ErrNotFound when it does not exist.
func (s *Store) DeleteAccount(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM accounts WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("store: delete account %d: %w", id, err)
	}
	return mustAffect(res, "account "+strconv.FormatInt(id, 10))
}
