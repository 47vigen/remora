-- Subscription accounts: remora's view of the OAuth engine's credentials. provider is the user-facing name
-- ("grok", not the engine's "xai"); credential is the engine's credential file name.
CREATE TABLE accounts (
  id         INTEGER PRIMARY KEY,
  provider   TEXT NOT NULL,
  label      TEXT NOT NULL DEFAULT '',
  credential TEXT NOT NULL UNIQUE,
  enabled    INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL
) STRICT;
