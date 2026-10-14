package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"
)

// ClientKey is a remora key a client presents on /v1. Its value is never stored, only its MAC and a short Prefix
// to recognise it by. VKID and VKValue are the front door's virtual key for it (VKValue sealed at rest). Prefix
// and CreatedAt are set by the store; PutClientKey ignores them.
type ClientKey struct {
	ID, Name      string
	Prefix        string
	Allow         []string
	Enabled       bool
	VKID, VKValue string
	CreatedAt     time.Time
}

// keyPrefixLen is the most characters of a key value kept as its prefix.
const keyPrefixLen = 8

// KeyPrefix returns the part of a key value kept to recognise it: its first 8 characters, but never more than
// half of it, so a short caller-chosen value is not given away.
func KeyPrefix(value string) string {
	n := min(keyPrefixLen, len(value)/2)
	for n > 0 && !utf8.RuneStart(value[n]) {
		n-- // never split a character
	}
	return value[:n]
}

// createdAtLayout is how the schema's strftime('%Y-%m-%dT%H:%M:%fZ','now') default writes a time.
const createdAtLayout = "2006-01-02T15:04:05.999Z"

func clientKeyAAD(id string) string { return "client_key:" + id }

const clientKeyCols = "id, name, prefix, allow, enabled, vk_id, vk_secret, created_at"

// scanClientKey scans clientKeyCols and opens the virtual key value.
func scanClientKey(sc interface{ Scan(...any) error }, sl *Sealer) (ClientKey, error) {
	var (
		k        ClientKey
		allow    string
		vkSecret []byte
		created  string
	)
	if err := sc.Scan(&k.ID, &k.Name, &k.Prefix, &allow, &k.Enabled, &k.VKID, &vkSecret, &created); err != nil {
		return ClientKey{}, err
	}
	var err error
	if k.Allow, err = decodeList(allow); err != nil {
		return ClientKey{}, fmt.Errorf("store: client key %s: %w", k.ID, err)
	}
	if k.CreatedAt, err = time.Parse(createdAtLayout, created); err != nil {
		return ClientKey{}, fmt.Errorf("store: client key %s: created_at: %w", k.ID, err)
	}
	if vkSecret != nil {
		if k.VKValue, err = sl.Open(vkSecret, clientKeyAAD(k.ID)); err != nil {
			return ClientKey{}, err
		}
	}
	return k, nil
}

// ClientKeyByValue finds the client key whose value is value, enabled or not. ErrNotFound when none matches.
func (s *Store) ClientKeyByValue(ctx context.Context, value string) (ClientKey, error) {
	sl := s.sealer.Load()
	if sl == nil {
		return ClientKey{}, ErrNoSealer
	}
	row := s.db.QueryRowContext(ctx,
		"SELECT "+clientKeyCols+" FROM client_keys WHERE key_mac = ?", sl.MAC([]byte(value)))
	k, err := scanClientKey(row, sl)
	if errors.Is(err, sql.ErrNoRows) {
		return ClientKey{}, ErrNotFound
	}
	if err != nil {
		return ClientKey{}, fmt.Errorf("store: client key by value: %w", err)
	}
	return k, nil
}

// ClientKeyVKIDs maps client key id to its virtual key id, for keys that have one.
func (s *Store) ClientKeyVKIDs(ctx context.Context) (map[string]string, error) {
	return stringMap(ctx, s.db, "SELECT id, vk_id FROM client_keys WHERE vk_id != ''")
}

// ClientKeys lists the client keys by name, virtual key values opened.
func (tx *Tx) ClientKeys(ctx context.Context) ([]ClientKey, error) {
	if tx.sealer == nil {
		return nil, ErrNoSealer
	}
	rows, err := tx.tx.QueryContext(ctx, "SELECT "+clientKeyCols+" FROM client_keys ORDER BY name")
	if err != nil {
		return nil, fmt.Errorf("store: client keys: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []ClientKey
	for rows.Next() {
		k, err := scanClientKey(rows, tx.sealer)
		if err != nil {
			return nil, fmt.Errorf("store: client keys: %w", err)
		}
		out = append(out, k)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: client keys: %w", err)
	}
	return out, nil
}

// PutClientKey creates or updates the client key k.ID (an empty ID creates a key with a new UUID v4) and returns
// it as stored. value is caller-chosen and only its MAC and prefix are stored; value "" on an existing key keeps the stored
// MAC, and a new key needs a value. The virtual key fields are not changed here (see SetClientKeyVK).
func (tx *Tx) PutClientKey(ctx context.Context, k ClientKey, value string) (ClientKey, error) {
	if tx.sealer == nil {
		return ClientKey{}, ErrNoSealer
	}
	if k.ID == "" {
		k.ID = newID()
	}
	var (
		res sql.Result
		err error
	)
	if value != "" {
		res, err = tx.tx.ExecContext(ctx, `
INSERT INTO client_keys (id, name, key_mac, prefix, allow, enabled) VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (id) DO UPDATE SET
  name = excluded.name,
  key_mac = excluded.key_mac,
  prefix = excluded.prefix,
  allow = excluded.allow,
  enabled = excluded.enabled,
  updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now')`,
			k.ID, k.Name, tx.sealer.MAC([]byte(value)), KeyPrefix(value), encodeList(k.Allow), boolInt(k.Enabled))
	} else {
		res, err = tx.tx.ExecContext(ctx, `
UPDATE client_keys SET name = ?, allow = ?, enabled = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now')
WHERE id = ?`, k.Name, encodeList(k.Allow), boolInt(k.Enabled), k.ID)
	}
	if err != nil {
		return ClientKey{}, fmt.Errorf("store: put client key %q: %w", k.Name, mapErr(err))
	}
	if value == "" {
		if err := mustAffect(res, "client key "+k.ID+" (a new key needs a value)"); err != nil {
			return ClientKey{}, err
		}
	}
	row := tx.tx.QueryRowContext(ctx, "SELECT "+clientKeyCols+" FROM client_keys WHERE id = ?", k.ID)
	out, err := scanClientKey(row, tx.sealer)
	if err != nil {
		return ClientKey{}, fmt.Errorf("store: put client key %q: %w", k.Name, err)
	}
	return out, nil
}

// SetClientKeyVK records the front door's virtual key for a client key; vkValue is sealed. ErrNotFound when the
// client key does not exist.
func (tx *Tx) SetClientKeyVK(ctx context.Context, id, vkID, vkValue string) error {
	if tx.sealer == nil {
		return ErrNoSealer
	}
	var sealed []byte
	if vkValue != "" {
		var err error
		if sealed, err = tx.sealer.Seal(vkValue, clientKeyAAD(id)); err != nil {
			return err
		}
	}
	res, err := tx.tx.ExecContext(ctx,
		"UPDATE client_keys SET vk_id = ?, vk_secret = ? WHERE id = ?", vkID, sealed, id)
	if err != nil {
		return fmt.Errorf("store: set client key %s virtual key: %w", id, err)
	}
	return mustAffect(res, "client key "+id)
}

// DeleteClientKey deletes a client key. ErrNotFound when it does not exist.
func (tx *Tx) DeleteClientKey(ctx context.Context, id string) error {
	res, err := tx.tx.ExecContext(ctx, "DELETE FROM client_keys WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("store: delete client key %s: %w", id, err)
	}
	return mustAffect(res, "client key "+id)
}

// stringMap runs a two-column query and returns it as a map.
func stringMap(ctx context.Context, q dbtx, query string, args ...any) (map[string]string, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, fmt.Errorf("store: %w", err)
		}
		out[k] = v
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	return out, nil
}
