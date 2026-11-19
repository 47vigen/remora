-- Count input writes in the same transaction, including cascades and writes outside Store.Write.
CREATE TABLE store_generation (generation INTEGER NOT NULL) STRICT;
INSERT INTO store_generation VALUES (0);

CREATE TRIGGER generation_providers_insert AFTER INSERT ON providers BEGIN UPDATE store_generation SET generation = generation + 1; END;
CREATE TRIGGER generation_providers_update AFTER UPDATE ON providers BEGIN UPDATE store_generation SET generation = generation + 1; END;
CREATE TRIGGER generation_providers_delete AFTER DELETE ON providers BEGIN UPDATE store_generation SET generation = generation + 1; END;

CREATE TRIGGER generation_provider_keys_insert AFTER INSERT ON provider_keys BEGIN UPDATE store_generation SET generation = generation + 1; END;
CREATE TRIGGER generation_provider_keys_update AFTER UPDATE ON provider_keys BEGIN UPDATE store_generation SET generation = generation + 1; END;
CREATE TRIGGER generation_provider_keys_delete AFTER DELETE ON provider_keys BEGIN UPDATE store_generation SET generation = generation + 1; END;

-- Recording engine identifiers and MACs is not an input write.
CREATE TRIGGER generation_aliases_insert AFTER INSERT ON aliases BEGIN UPDATE store_generation SET generation = generation + 1; END;
CREATE TRIGGER generation_aliases_update AFTER UPDATE OF name, kind, capabilities, context_window, strategy ON aliases
BEGIN UPDATE store_generation SET generation = generation + 1; END;
CREATE TRIGGER generation_aliases_delete AFTER DELETE ON aliases BEGIN UPDATE store_generation SET generation = generation + 1; END;

CREATE TRIGGER generation_alias_members_insert AFTER INSERT ON alias_members BEGIN UPDATE store_generation SET generation = generation + 1; END;
CREATE TRIGGER generation_alias_members_update AFTER UPDATE ON alias_members BEGIN UPDATE store_generation SET generation = generation + 1; END;
CREATE TRIGGER generation_alias_members_delete AFTER DELETE ON alias_members BEGIN UPDATE store_generation SET generation = generation + 1; END;

CREATE TRIGGER generation_client_keys_insert AFTER INSERT ON client_keys BEGIN UPDATE store_generation SET generation = generation + 1; END;
CREATE TRIGGER generation_client_keys_update AFTER UPDATE OF id, name, key_mac, allow, enabled, prefix ON client_keys
BEGIN UPDATE store_generation SET generation = generation + 1; END;
CREATE TRIGGER generation_client_keys_delete AFTER DELETE ON client_keys BEGIN UPDATE store_generation SET generation = generation + 1; END;

CREATE TRIGGER generation_accounts_insert AFTER INSERT ON accounts BEGIN UPDATE store_generation SET generation = generation + 1; END;
CREATE TRIGGER generation_accounts_update AFTER UPDATE ON accounts BEGIN UPDATE store_generation SET generation = generation + 1; END;
CREATE TRIGGER generation_accounts_delete AFTER DELETE ON accounts BEGIN UPDATE store_generation SET generation = generation + 1; END;

CREATE TRIGGER generation_account_strategies_insert AFTER INSERT ON account_strategies
BEGIN UPDATE store_generation SET generation = generation + 1; END;
CREATE TRIGGER generation_account_strategies_update AFTER UPDATE ON account_strategies
BEGIN UPDATE store_generation SET generation = generation + 1; END;
CREATE TRIGGER generation_account_strategies_delete AFTER DELETE ON account_strategies
BEGIN UPDATE store_generation SET generation = generation + 1; END;

-- Only the OAuth engine's routing access key is read when reconciling its settings.
CREATE TRIGGER generation_oauth_key_insert AFTER INSERT ON engine_state
WHEN NEW.engine = 'cliproxyapi' AND NEW.key = 'api_key'
BEGIN UPDATE store_generation SET generation = generation + 1; END;
CREATE TRIGGER generation_oauth_key_update AFTER UPDATE OF engine, key, value ON engine_state
WHEN (OLD.engine = 'cliproxyapi' AND OLD.key = 'api_key') OR (NEW.engine = 'cliproxyapi' AND NEW.key = 'api_key')
BEGIN UPDATE store_generation SET generation = generation + 1; END;
CREATE TRIGGER generation_oauth_key_delete AFTER DELETE ON engine_state
WHEN OLD.engine = 'cliproxyapi' AND OLD.key = 'api_key'
BEGIN UPDATE store_generation SET generation = generation + 1; END;
