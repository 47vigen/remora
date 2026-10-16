package store

import (
	"context"
	"fmt"
	"maps"

	"github.com/47vigen/remora/internal/alias"
)

// snapshot is the client keys and aliases every /v1 request reads, held in memory so no request touches SQLite.
// It is rebuilt after every committed Write; its maps and their values are shared and never modified.
type snapshot struct {
	keys    map[string]ClientKey // by string(Sealer.MAC(value)), VKValue opened
	aliases map[string]alias.Alias
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
	for rows.Next() {
		var mac []byte
		k, err := scanClientKey(withExtra{rows, &mac}, sl)
		if err != nil {
			return nil, fmt.Errorf("client keys: %w", err)
		}
		keys[string(mac)] = k
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("client keys: %w", err)
	}
	aliases, err := loadAliases(ctx, s.db)
	if err != nil {
		return nil, err
	}
	return &snapshot{keys: keys, aliases: aliases}, nil
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
