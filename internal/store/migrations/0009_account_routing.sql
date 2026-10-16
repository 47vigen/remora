ALTER TABLE accounts ADD COLUMN priority INTEGER NOT NULL DEFAULT 0;
-- One strategy per subscription provider (user-facing name); a provider without a row uses round-robin.
CREATE TABLE account_strategies (provider TEXT PRIMARY KEY, strategy TEXT NOT NULL) STRICT;
