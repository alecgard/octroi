-- Revert sessions tenant_id.
DROP INDEX IF EXISTS idx_sessions_tenant;
ALTER TABLE sessions DROP COLUMN IF EXISTS tenant_id;

-- Revert unique constraints to original (without tenant_id).
ALTER TABLE tool_rate_limits DROP CONSTRAINT IF EXISTS tool_rate_limits_tool_scope_tenant_key;
ALTER TABLE tool_rate_limits ADD CONSTRAINT tool_rate_limits_tool_id_scope_scope_id_key UNIQUE(tool_id, scope, scope_id);

ALTER TABLE agent_tool_permissions DROP CONSTRAINT IF EXISTS agent_tool_permissions_agent_tool_tenant_key;
ALTER TABLE agent_tool_permissions ADD CONSTRAINT agent_tool_permissions_agent_id_tool_id_key UNIQUE(agent_id, tool_id);

ALTER TABLE agent_tool_budgets DROP CONSTRAINT IF EXISTS agent_tool_budgets_agent_tool_tenant_key;
ALTER TABLE agent_tool_budgets ADD CONSTRAINT agent_tool_budgets_agent_id_tool_id_key UNIQUE(agent_id, tool_id);
