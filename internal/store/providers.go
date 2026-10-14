package store

import (
	"context"
	"fmt"
)

// ProviderKind is how remora drives a provider: "builtin" (a provider the front door ships, named by Type),
// "openai-compatible", "typesafe-compatible" (decision endpoints) or "soniox".
type ProviderKind string

// Provider kinds.
const (
	KindBuiltin            ProviderKind = "builtin"
	KindOpenAICompatible   ProviderKind = "openai-compatible"
	KindTypeSafeCompatible ProviderKind = "typesafe-compatible"
	KindSoniox             ProviderKind = "soniox"
)

// Provider is an upstream remora routes to.
type Provider struct {
	Name                string
	Kind                ProviderKind
	Type                string // builtin only: the front door's provider, e.g. "openrouter"
	BaseURL             string
	AllowPrivateNetwork bool
}

// ProviderKey is one credential of a provider. Secret is in clear in memory only; at rest it is sealed.
type ProviderKey struct {
	ID, Provider, Name, Secret string
	Enabled                    bool
}

func providerKeyAAD(id string) string { return "provider_key:" + id }

// Providers lists the providers by name.
func (tx *Tx) Providers(ctx context.Context) ([]Provider, error) {
	rows, err := tx.tx.QueryContext(ctx,
		"SELECT name, kind, type, base_url, allow_private_network FROM providers ORDER BY name")
	if err != nil {
		return nil, fmt.Errorf("store: providers: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Provider
	for rows.Next() {
		var p Provider
		if err := rows.Scan(&p.Name, &p.Kind, &p.Type, &p.BaseURL, &p.AllowPrivateNetwork); err != nil {
			return nil, fmt.Errorf("store: providers: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: providers: %w", err)
	}
	return out, nil
}

// PutProvider creates or replaces the provider p.Name. Its keys are kept.
func (tx *Tx) PutProvider(ctx context.Context, p Provider) error {
	_, err := tx.tx.ExecContext(ctx, `
INSERT INTO providers (name, kind, type, base_url, allow_private_network) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (name) DO UPDATE SET
  kind = excluded.kind,
  type = excluded.type,
  base_url = excluded.base_url,
  allow_private_network = excluded.allow_private_network,
  updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now')`,
		p.Name, string(p.Kind), p.Type, p.BaseURL, boolInt(p.AllowPrivateNetwork))
	if err != nil {
		return fmt.Errorf("store: put provider %q: %w", p.Name, mapErr(err))
	}
	return nil
}

// DeleteProvider deletes the provider and its keys. ErrNotFound when it does not exist.
func (tx *Tx) DeleteProvider(ctx context.Context, name string) error {
	res, err := tx.tx.ExecContext(ctx, "DELETE FROM providers WHERE name = ?", name)
	if err != nil {
		return fmt.Errorf("store: delete provider %q: %w", name, err)
	}
	return mustAffect(res, "provider "+name)
}

// ProviderKeys lists every provider key, secrets opened, by provider and name.
func (tx *Tx) ProviderKeys(ctx context.Context) ([]ProviderKey, error) {
	if tx.sealer == nil {
		return nil, ErrNoSealer
	}
	rows, err := tx.tx.QueryContext(ctx,
		"SELECT id, provider, name, secret, enabled FROM provider_keys ORDER BY provider, name")
	if err != nil {
		return nil, fmt.Errorf("store: provider keys: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []ProviderKey
	for rows.Next() {
		var (
			k      ProviderKey
			sealed []byte
		)
		if err := rows.Scan(&k.ID, &k.Provider, &k.Name, &sealed, &k.Enabled); err != nil {
			return nil, fmt.Errorf("store: provider keys: %w", err)
		}
		if k.Secret, err = tx.sealer.Open(sealed, providerKeyAAD(k.ID)); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: provider keys: %w", err)
	}
	return out, nil
}

// PutProviderKey creates or replaces the key k.ID; an empty ID creates a key with a new UUID v4.
func (tx *Tx) PutProviderKey(ctx context.Context, k ProviderKey) error {
	if tx.sealer == nil {
		return ErrNoSealer
	}
	if k.ID == "" {
		k.ID = newID()
	}
	sealed, err := tx.sealer.Seal(k.Secret, providerKeyAAD(k.ID))
	if err != nil {
		return err
	}
	_, err = tx.tx.ExecContext(ctx, `
INSERT INTO provider_keys (id, provider, name, secret, enabled) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (id) DO UPDATE SET
  provider = excluded.provider,
  name = excluded.name,
  secret = excluded.secret,
  enabled = excluded.enabled`,
		k.ID, k.Provider, k.Name, sealed, boolInt(k.Enabled))
	if err != nil {
		return fmt.Errorf("store: put provider key %s/%s: %w", k.Provider, k.Name, mapErr(err))
	}
	return nil
}

// DeleteProviderKey deletes a provider key. ErrNotFound when it does not exist.
func (tx *Tx) DeleteProviderKey(ctx context.Context, id string) error {
	res, err := tx.tx.ExecContext(ctx, "DELETE FROM provider_keys WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("store: delete provider key %s: %w", id, err)
	}
	return mustAffect(res, "provider key "+id)
}

// ProviderKeysByKind returns the plaintext secrets of the enabled keys of every provider of that kind, never nil.
func (s *Store) ProviderKeysByKind(ctx context.Context, kind ProviderKind) ([]string, error) {
	sl := s.sealer.Load()
	if sl == nil {
		return nil, ErrNoSealer
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT k.id, k.secret FROM provider_keys k JOIN providers p ON p.name = k.provider
WHERE p.kind = ? AND k.enabled = 1 ORDER BY k.provider, k.name`, string(kind))
	if err != nil {
		return nil, fmt.Errorf("store: provider keys of kind %s: %w", kind, err)
	}
	defer func() { _ = rows.Close() }()
	out := []string{}
	for rows.Next() {
		var (
			id     string
			sealed []byte
		)
		if err := rows.Scan(&id, &sealed); err != nil {
			return nil, fmt.Errorf("store: provider keys of kind %s: %w", kind, err)
		}
		secret, err := sl.Open(sealed, providerKeyAAD(id))
		if err != nil {
			return nil, err
		}
		out = append(out, secret)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: provider keys of kind %s: %w", kind, err)
	}
	return out, nil
}
