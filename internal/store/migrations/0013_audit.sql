CREATE TABLE audit (
  id INTEGER PRIMARY KEY,
  at INTEGER NOT NULL,
  principal TEXT NOT NULL CHECK (principal IN ('session', 'token')),
  token_id TEXT NOT NULL DEFAULT '',
  token_name TEXT NOT NULL DEFAULT '',
  operation_id TEXT NOT NULL,
  diff TEXT NOT NULL CHECK (json_valid(diff))
) STRICT;

CREATE INDEX audit_operation_id ON audit (operation_id, id DESC);
