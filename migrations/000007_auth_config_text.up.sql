-- Change auth_config from JSONB to TEXT to support encrypted (base64) values.
ALTER TABLE tools ALTER COLUMN auth_config TYPE TEXT USING auth_config::TEXT;
ALTER TABLE tools ALTER COLUMN auth_config SET DEFAULT '{}';
