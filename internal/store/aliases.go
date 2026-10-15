package store

import (
	"context"
	"fmt"

	"github.com/47vigen/remora/internal/alias"
)

// Aliases returns every alias by name, members in order.
func (s *Store) Aliases(ctx context.Context) (map[string]alias.Alias, error) {
	return loadAliases(ctx, s.db)
}

// AliasRuleIDs maps alias name to the front door's routing rule id, for aliases that have one.
func (s *Store) AliasRuleIDs(ctx context.Context) (map[string]string, error) {
	return stringMap(ctx, s.db, "SELECT name, engine_rule_id FROM aliases WHERE engine_rule_id != ''")
}

// AliasRuleIDs maps alias name to the front door's routing rule id, as this transaction sees it (its own
// uncommitted writes included).
func (tx *Tx) AliasRuleIDs(ctx context.Context) (map[string]string, error) {
	return stringMap(ctx, tx.tx, "SELECT name, engine_rule_id FROM aliases WHERE engine_rule_id != ''")
}

// Aliases returns every alias by name, members in order.
func (tx *Tx) Aliases(ctx context.Context) (map[string]alias.Alias, error) {
	return loadAliases(ctx, tx.tx)
}

func loadAliases(ctx context.Context, q dbtx) (map[string]alias.Alias, error) {
	rows, err := q.QueryContext(ctx, "SELECT name, kind, capabilities, context_window, strategy FROM aliases")
	if err != nil {
		return nil, fmt.Errorf("store: aliases: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]alias.Alias{}
	for rows.Next() {
		var (
			a    alias.Alias
			caps string
		)
		if err := rows.Scan(&a.Name, &a.Kind, &caps, &a.ContextWindow, &a.Strategy); err != nil {
			return nil, fmt.Errorf("store: aliases: %w", err)
		}
		if a.Capabilities, err = decodeList(caps); err != nil {
			return nil, fmt.Errorf("store: alias %q: %w", a.Name, err)
		}
		out[a.Name] = a
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: aliases: %w", err)
	}

	mrows, err := q.QueryContext(ctx, "SELECT alias, ref FROM alias_members ORDER BY alias, position")
	if err != nil {
		return nil, fmt.Errorf("store: alias members: %w", err)
	}
	defer func() { _ = mrows.Close() }()
	for mrows.Next() {
		var name, ref string
		if err := mrows.Scan(&name, &ref); err != nil {
			return nil, fmt.Errorf("store: alias members: %w", err)
		}
		a := out[name]
		a.Members = append(a.Members, alias.Member{Ref: ref})
		out[name] = a
	}
	if err := mrows.Err(); err != nil {
		return nil, fmt.Errorf("store: alias members: %w", err)
	}
	return out, nil
}

// PutAlias creates or replaces the alias a.Name and replaces its members in order. The routing rule id is kept.
func (tx *Tx) PutAlias(ctx context.Context, a alias.Alias) error {
	if err := alias.ValidName(a.Name); err != nil {
		return fmt.Errorf("store: put alias: %w", err)
	}
	_, err := tx.tx.ExecContext(ctx, `
INSERT INTO aliases (name, kind, capabilities, context_window, strategy) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (name) DO UPDATE SET
  kind = excluded.kind,
  capabilities = excluded.capabilities,
  context_window = excluded.context_window,
  strategy = excluded.strategy,
  updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now')`,
		a.Name, string(a.Kind), encodeList(a.Capabilities), a.ContextWindow, string(a.Strategy))
	if err != nil {
		return fmt.Errorf("store: put alias %q: %w", a.Name, mapErr(err))
	}
	if _, err := tx.tx.ExecContext(ctx, "DELETE FROM alias_members WHERE alias = ?", a.Name); err != nil {
		return fmt.Errorf("store: put alias %q: clear members: %w", a.Name, err)
	}
	for i, m := range a.Members {
		if _, err := tx.tx.ExecContext(ctx,
			"INSERT INTO alias_members (alias, position, ref) VALUES (?, ?, ?)", a.Name, i, m.Ref); err != nil {
			return fmt.Errorf("store: put alias %q: member %d: %w", a.Name, i, err)
		}
	}
	return nil
}

// DeleteAlias deletes an alias and its members. ErrNotFound when it does not exist.
func (tx *Tx) DeleteAlias(ctx context.Context, name string) error {
	res, err := tx.tx.ExecContext(ctx, "DELETE FROM aliases WHERE name = ?", name)
	if err != nil {
		return fmt.Errorf("store: delete alias %q: %w", name, err)
	}
	return mustAffect(res, "alias "+name)
}

// SetAliasRuleID records the front door's routing rule id for an alias. ErrNotFound when the alias does not exist.
func (tx *Tx) SetAliasRuleID(ctx context.Context, name, ruleID string) error {
	res, err := tx.tx.ExecContext(ctx, "UPDATE aliases SET engine_rule_id = ? WHERE name = ?", ruleID, name)
	if err != nil {
		return fmt.Errorf("store: set alias %q rule id: %w", name, err)
	}
	return mustAffect(res, "alias "+name)
}
