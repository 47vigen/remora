ALTER TABLE api_tokens ADD COLUMN scope TEXT NOT NULL DEFAULT 'admin' CHECK (scope IN ('read', 'operate', 'admin'));
ALTER TABLE api_tokens ADD COLUMN expires_at INTEGER;
