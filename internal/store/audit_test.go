package store

import (
	"testing"
	"time"
)

func TestAuditDoesNotChangeGeneration(t *testing.T) {
	s := openTemp(t)
	ctx := t.Context()
	generation := func() int64 {
		t.Helper()
		var n int64
		if err := s.DB().QueryRowContext(ctx, "SELECT generation FROM store_generation").Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := generation()
	if err := s.RecordAudit(ctx, AuditEntry{
		At: time.Now(), Principal: "session", OperationID: "createKey", Diff: map[string]any{"name": "example"},
	}); err != nil {
		t.Fatal(err)
	}
	if after := generation(); after != before {
		t.Errorf("audit insert moved generation from %d to %d", before, after)
	}
}
