-- One quota snapshot per subscription account, overwritten on every check. windows is a JSON array; times use the
-- accounts table's format and '' means never.
CREATE TABLE account_quota (
  account_id    INTEGER PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
  plan          TEXT NOT NULL DEFAULT '',
  windows       TEXT NOT NULL DEFAULT '[]',
  limit_reached INTEGER NOT NULL DEFAULT 0,
  source        TEXT NOT NULL DEFAULT 'poll',
  fetched_at    TEXT NOT NULL DEFAULT '',
  checked_at    TEXT NOT NULL,
  error         TEXT NOT NULL DEFAULT ''
) STRICT;
