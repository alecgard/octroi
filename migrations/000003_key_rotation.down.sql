ALTER TABLE agents
    DROP COLUMN prev_key_hash,
    DROP COLUMN prev_key_expires_at,
    DROP COLUMN api_key_suffix,
    DROP COLUMN prev_key_prefix;
