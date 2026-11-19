package store

import (
	"cmp"
	"context"
	"database/sql"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/47vigen/remora/internal/alias"
)

// snapshot holds client keys, aliases and the log redactor in memory so reads do not touch SQLite.
// Its maps and their values are shared and never modified.
type snapshot struct {
	keys    map[string]ClientKey // by string(Sealer.MAC(value)), VKValue opened
	aliases map[string]alias.Alias
	secrets *strings.Replacer
}

// Refresh rebuilds the snapshot from the committed database, unless a reconcile write is still pending.
// On failure it drops the snapshot and returns the error; reads fall back to SQL. Without a sealer there is nothing to rebuild.
func (s *Store) Refresh(ctx context.Context) error {
	s.snapMu.Lock()
	defer s.snapMu.Unlock()
	if s.pending {
		return nil
	}
	return s.refreshSnapshot(ctx)
}

// beginPending pins a snapshot before the unaccepted rows can be committed.
func (s *Store) beginPending(ctx context.Context) error {
	s.snapMu.Lock()
	defer s.snapMu.Unlock()
	if s.pending {
		return fmt.Errorf("store: a reconcile write is already pending")
	}
	if s.snap.Load() == nil {
		if s.sealer.Load() == nil {
			return ErrNoSealer
		}
		if err := s.refreshSnapshot(ctx); err != nil {
			return err
		}
	}
	s.pending = true
	return nil
}

func (s *Store) finishPending(ctx context.Context) {
	s.snapMu.Lock()
	defer s.snapMu.Unlock()
	s.pending = false
	_ = s.refreshSnapshot(context.WithoutCancel(ctx))
}

// refreshSnapshot requires snapMu to be held.
func (s *Store) refreshSnapshot(ctx context.Context) error {
	sl := s.sealer.Load()
	if sl == nil {
		s.snap.Store(nil)
		return nil
	}
	snap, err := s.loadSnapshot(ctx, sl)
	if err != nil {
		s.snap.Store(nil)
		return fmt.Errorf("store: refresh: %w", err)
	}
	s.snap.Store(snap)
	return nil
}

func (s *Store) loadSnapshot(ctx context.Context, sl *Sealer) (*snapshot, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+clientKeyCols+", key_mac FROM client_keys")
	if err != nil {
		return nil, fmt.Errorf("client keys: %w", err)
	}
	defer func() { _ = rows.Close() }()
	keys := map[string]ClientKey{}
	var secrets []string
	for rows.Next() {
		var mac []byte
		k, err := scanClientKey(withExtra{rows, &mac}, sl)
		if err != nil {
			return nil, fmt.Errorf("client keys: %w", err)
		}
		keys[string(mac)] = k
		if k.VKValue != "" {
			secrets = append(secrets, k.VKValue)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("client keys: %w", err)
	}
	aliases, err := loadAliases(ctx, s.db)
	if err != nil {
		return nil, err
	}
	secrets, err = s.loadLogSecrets(ctx, sl, secrets)
	if err != nil {
		return nil, err
	}
	// Prefer the longest match when one stored secret is a prefix of another.
	slices.SortFunc(secrets, func(a, b string) int { return cmp.Compare(len(b), len(a)) })
	pairs := make([]string, 0, 2*len(secrets))
	for _, secret := range secrets {
		pairs = append(pairs, secret, "[redacted]")
	}
	matcher := strings.NewReplacer(pairs...)
	_ = matcher.Replace("") // compile once, before publishing the snapshot
	return &snapshot{keys: keys, aliases: aliases, secrets: matcher}, nil
}

// withExtra scans one more column after the ones its caller asks for.
type withExtra struct {
	sc    interface{ Scan(...any) error }
	extra any
}

func (w withExtra) Scan(dest ...any) error { return w.sc.Scan(append(dest, w.extra)...) }

// snapshotForRead keeps a SQL fallback from crossing the start of a pending write.
func (s *Store) snapshotForRead() (*snapshot, func()) {
	if snap := s.snap.Load(); snap != nil {
		return snap, func() {}
	}
	s.snapMu.Lock()
	return s.snap.Load(), s.snapMu.Unlock
}

// Alias returns the alias named name, without copying the whole set.
func (s *Store) Alias(ctx context.Context, name string) (alias.Alias, bool, error) {
	snap, unlock := s.snapshotForRead()
	defer unlock()
	if snap != nil {
		a, ok := snap.aliases[name]
		return a, ok, nil
	}
	all, err := loadAliases(ctx, s.db)
	if err != nil {
		return alias.Alias{}, false, err
	}
	a, ok := all[name]
	return a, ok, nil
}

// cloneAliases is the snapshot's alias set as a map the caller may change.
func (snap *snapshot) cloneAliases() map[string]alias.Alias { return maps.Clone(snap.aliases) }

// RedactSecrets masks recoverable stored credentials in an unencoded engine log line.
// With no snapshot, omit the line rather than risk logging a credential.
func (s *Store) RedactSecrets(text string) string {
	if snap := s.snap.Load(); snap != nil {
		return snap.secrets.Replace(text)
	}
	return "[redacted]"
}

func (s *Store) loadLogSecrets(ctx context.Context, sl *Sealer, secrets []string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
 SELECT NULL, id, secret FROM provider_keys
 UNION ALL
 SELECT engine, key, value FROM engine_state WHERE key IN ('admin_password', 'encryption_key', 'management_secret', 'api_key')`)
	if err != nil {
		return nil, fmt.Errorf("log secrets: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var engine sql.NullString
		var id string
		var value []byte
		if err := rows.Scan(&engine, &id, &value); err != nil {
			return nil, fmt.Errorf("log secrets: %w", err)
		}
		var secret string
		if engine.Valid {
			secret, err = sl.openEngineValue(engine.String, id, string(value))
		} else {
			secret, err = sl.Open(value, providerKeyAAD(id))
		}
		if err != nil {
			return nil, fmt.Errorf("log secrets: %w", err)
		}
		if secret != "" {
			secrets = append(secrets, secret)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("log secrets: %w", err)
	}
	return secrets, nil
}
