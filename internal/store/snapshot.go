package store

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/47vigen/remora/internal/alias"
)

// snapshot holds client keys, aliases and the log redactor in memory so reads do not touch SQLite.
// It is rebuilt after every committed Write; its maps and their values are shared and never modified.
type snapshot struct {
	keys    map[string]ClientKey // by string(Sealer.MAC(value)), VKValue opened
	aliases map[string]alias.Alias
	secrets *strings.Replacer
}

// Refresh rebuilds the snapshot from the committed database. Rebuilds are serialised, so the last one to run reads
// the last commit. On failure the snapshot is dropped, reads fall back to SQL (and fail there too, closed), and the
// error is returned. Without a sealer there is nothing to rebuild.
func (s *Store) Refresh(ctx context.Context) error {
	s.snapMu.Lock()
	defer s.snapMu.Unlock()
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

// Alias returns the alias named name, without copying the whole set.
func (s *Store) Alias(ctx context.Context, name string) (alias.Alias, bool, error) {
	if snap := s.snap.Load(); snap != nil {
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
 SELECT id, secret FROM provider_keys
 UNION ALL
 SELECT '', value FROM engine_state WHERE key IN ('admin_password', 'encryption_key', 'management_secret', 'api_key')`)
	if err != nil {
		return nil, fmt.Errorf("log secrets: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id string
		var value []byte
		if err := rows.Scan(&id, &value); err != nil {
			return nil, fmt.Errorf("log secrets: %w", err)
		}
		secret := string(value)
		if id != "" {
			secret, err = sl.Open(value, providerKeyAAD(id))
			if err != nil {
				return nil, fmt.Errorf("log secrets: %w", err)
			}
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
