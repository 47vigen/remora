CREATE TABLE providers (
  name                  TEXT PRIMARY KEY,
  kind                  TEXT NOT NULL CHECK (kind IN ('builtin','openai-compatible','typesafe-compatible','soniox')),
  type                  TEXT NOT NULL DEFAULT '',
  base_url              TEXT NOT NULL DEFAULT '',
  allow_private_network INTEGER NOT NULL DEFAULT 0,
  created_at            TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at            TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
) STRICT;
CREATE TABLE provider_keys (
  id       TEXT PRIMARY KEY,
  provider TEXT NOT NULL REFERENCES providers(name) ON DELETE CASCADE,
  name     TEXT NOT NULL,
  secret   BLOB NOT NULL,
  enabled  INTEGER NOT NULL DEFAULT 1,
  UNIQUE (provider, name)
) STRICT;
CREATE TABLE client_keys (
  id         TEXT PRIMARY KEY,
  name       TEXT NOT NULL UNIQUE,
  key_mac    BLOB NOT NULL UNIQUE,
  allow      TEXT NOT NULL DEFAULT '[]',
  enabled    INTEGER NOT NULL DEFAULT 1,
  vk_id      TEXT NOT NULL DEFAULT '',
  vk_secret  BLOB,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
) STRICT;
CREATE TABLE aliases (
  name           TEXT PRIMARY KEY,
  kind           TEXT NOT NULL CHECK (kind IN ('chat','embeddings','transcription','decisions')),
  capabilities   TEXT NOT NULL DEFAULT '[]',
  context_window INTEGER NOT NULL DEFAULT 0,
  engine_rule_id TEXT NOT NULL DEFAULT '',
  created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
) STRICT;
CREATE TABLE alias_members (
  alias    TEXT NOT NULL REFERENCES aliases(name) ON DELETE CASCADE,
  position INTEGER NOT NULL,
  ref      TEXT NOT NULL,
  PRIMARY KEY (alias, position)
) STRICT;
-- object: "provider:<name>" | "key:<id>" | "vk:<client_key_id>" | "rule:<alias>"
CREATE TABLE engine_sync (
  object TEXT PRIMARY KEY,
  mac    TEXT NOT NULL
) STRICT;
