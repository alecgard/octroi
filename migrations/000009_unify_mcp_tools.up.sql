-- Add MCP-specific columns to the tools table.
ALTER TABLE tools ADD COLUMN transport TEXT;
ALTER TABLE tools ADD COLUMN enabled BOOLEAN NOT NULL DEFAULT TRUE;

-- Migrate existing MCP upstreams into the tools table.
INSERT INTO tools (id, name, description, endpoint, mode, auth_type, auth_config, transport, enabled, created_at, updated_at)
SELECT id::uuid, name, name, endpoint, 'mcp', auth_type, auth_config, transport, enabled, created_at, updated_at
FROM mcp_upstreams;

-- Drop the separate MCP upstreams table.
DROP TABLE mcp_upstreams;
