CREATE TABLE sessions (
  hash       BLOB PRIMARY KEY,
  expires_at INTEGER NOT NULL
) STRICT;
CREATE TABLE api_tokens (
  id           TEXT PRIMARY KEY,
  name         TEXT NOT NULL,
  prefix       TEXT NOT NULL,
  hash         BLOB NOT NULL UNIQUE,
  created_at   INTEGER NOT NULL,
  last_used_at INTEGER
) STRICT;
