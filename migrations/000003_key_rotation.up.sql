ALTER TABLE agents
    ADD COLUMN prev_key_hash TEXT,
    ADD COLUMN prev_key_expires_at TIMESTAMPTZ;
