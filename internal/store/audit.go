package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// AuditEntry records an authenticated management action. Diff contains only audit-safe fields.
type AuditEntry struct {
	ID                 int64
	At                 time.Time
	Principal          string
	TokenID, TokenName string
	OperationID        string
	Diff               map[string]any
}

// RecordAudit appends an entry without changing the configuration generation.
func (s *Store) RecordAudit(ctx context.Context, e AuditEntry) error {
	diff, err := json.Marshal(e.Diff)
	if err != nil {
		return fmt.Errorf("store: encode audit diff: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO audit (at, principal, token_id, token_name, operation_id, diff)
		VALUES (?, ?, ?, ?, ?, ?)`, e.At.UnixNano(), e.Principal, e.TokenID, e.TokenName, e.OperationID, string(diff))
	if err != nil {
		return fmt.Errorf("store: record audit entry: %w", err)
	}
	return nil
}

// ListAudit returns a newest-first page and the exact total for the operation filter (empty matches all).
func (s *Store) ListAudit(ctx context.Context, operation string, limit, offset int) ([]AuditEntry, int, error) {
	where := ""
	var args []any
	if operation != "" {
		where = " WHERE operation_id = ?"
		args = append(args, operation, operation)
	}
	// One SQLite snapshot for the count and page, even if the requested page is empty.
	rows, err := s.db.QueryContext(ctx, `SELECT total, id, at, principal, token_id, token_name, operation_id, diff
		FROM (SELECT count(*) AS total FROM audit`+where+`)
		LEFT JOIN (SELECT * FROM audit`+where+` ORDER BY id DESC LIMIT ? OFFSET ?) ON true
		ORDER BY id DESC`, append(args, limit, offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("store: list audit entries: %w", err)
	}
	defer func() { _ = rows.Close() }()
	items := []AuditEntry{}
	var total int
	for rows.Next() {
		var id, at sql.NullInt64
		var principal, tokenID, tokenName, op, diff sql.NullString
		if err := rows.Scan(&total, &id, &at, &principal, &tokenID, &tokenName, &op, &diff); err != nil {
			return nil, 0, err
		}
		if !id.Valid {
			break
		}
		e := AuditEntry{
			ID: id.Int64, At: time.Unix(0, at.Int64).UTC(), Principal: principal.String,
			TokenID: tokenID.String, TokenName: tokenName.String, OperationID: op.String,
		}
		if err := json.Unmarshal([]byte(diff.String), &e.Diff); err != nil {
			return nil, 0, fmt.Errorf("store: decode audit diff: %w", err)
		}
		items = append(items, e)
	}
	return items, total, rows.Err()
}
