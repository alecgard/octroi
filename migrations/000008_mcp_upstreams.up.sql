CREATE TABLE mcp_upstreams (
    id          TEXT PRIMARY KEY DEFAULT gen_random_uuid()::TEXT,
    name        TEXT NOT NULL UNIQUE,
    endpoint    TEXT NOT NULL,
    transport   TEXT NOT NULL DEFAULT 'streamable-http',
    auth_type   TEXT NOT NULL DEFAULT 'none',
    auth_config TEXT,
    enabled     BOOLEAN NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
