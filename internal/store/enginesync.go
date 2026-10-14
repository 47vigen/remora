package store

import (
	"context"
	"fmt"
)

// EngineMACs returns the MAC of every engine object as last pushed, keyed by object ("provider:<name>",
// "key:<id>", "vk:<client_key_id>", "rule:<alias>").
func (tx *Tx) EngineMACs(ctx context.Context) (map[string]string, error) {
	return stringMap(ctx, tx.tx, "SELECT object, mac FROM engine_sync")
}

// SetEngineMAC records the MAC of an engine object as pushed.
func (tx *Tx) SetEngineMAC(ctx context.Context, object, mac string) error {
	_, err := tx.tx.ExecContext(ctx, `
INSERT INTO engine_sync (object, mac) VALUES (?, ?)
ON CONFLICT (object) DO UPDATE SET mac = excluded.mac`, object, mac)
	if err != nil {
		return fmt.Errorf("store: set engine mac %s: %w", object, err)
	}
	return nil
}

// DeleteEngineMAC forgets an engine object. Deleting an unknown object is not an error.
func (tx *Tx) DeleteEngineMAC(ctx context.Context, object string) error {
	if _, err := tx.tx.ExecContext(ctx, "DELETE FROM engine_sync WHERE object = ?", object); err != nil {
		return fmt.Errorf("store: delete engine mac %s: %w", object, err)
	}
	return nil
}

// MAC returns HMAC-SHA256(macKey, data) with the store's Sealer, for the engine_sync records of objects that carry
// a secret. ErrNoSealer when the store has none.
func (tx *Tx) MAC(data []byte) ([]byte, error) {
	if tx.sealer == nil {
		return nil, ErrNoSealer
	}
	return tx.sealer.MAC(data), nil
}
