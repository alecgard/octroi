-- Recreate the mcp_upstreams table.
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

-- Move MCP tools back to mcp_upstreams.
INSERT INTO mcp_upstreams (id, name, endpoint, transport, auth_type, auth_config, enabled, created_at, updated_at)
SELECT id, name, endpoint, transport, auth_type, auth_config, enabled, created_at, updated_at
FROM tools WHERE mode = 'mcp';

-- Remove MCP tools from the tools table.
DELETE FROM tools WHERE mode = 'mcp';

-- Drop the MCP-specific columns.
ALTER TABLE tools DROP COLUMN transport;
ALTER TABLE tools DROP COLUMN enabled;
