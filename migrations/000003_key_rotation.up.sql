ALTER TABLE agents
    ADD COLUMN prev_key_hash TEXT,
    ADD COLUMN prev_key_expires_at TIMESTAMPTZ,
    ADD COLUMN api_key_suffix TEXT NOT NULL DEFAULT '',
    ADD COLUMN prev_key_prefix TEXT;
