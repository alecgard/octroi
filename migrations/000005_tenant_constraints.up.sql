-- Fix unique constraints to include tenant_id for proper multi-tenant isolation.

-- agent_tool_budgets: (agent_id, tool_id) → (agent_id, tool_id, tenant_id)
ALTER TABLE agent_tool_budgets DROP CONSTRAINT agent_tool_budgets_agent_id_tool_id_key;
ALTER TABLE agent_tool_budgets ADD CONSTRAINT agent_tool_budgets_agent_tool_tenant_key UNIQUE(agent_id, tool_id, tenant_id);

-- agent_tool_permissions: (agent_id, tool_id) → (agent_id, tool_id, tenant_id)
ALTER TABLE agent_tool_permissions DROP CONSTRAINT agent_tool_permissions_agent_id_tool_id_key;
ALTER TABLE agent_tool_permissions ADD CONSTRAINT agent_tool_permissions_agent_tool_tenant_key UNIQUE(agent_id, tool_id, tenant_id);

-- tool_rate_limits: (tool_id, scope, scope_id) → (tool_id, scope, scope_id, tenant_id)
ALTER TABLE tool_rate_limits DROP CONSTRAINT tool_rate_limits_tool_id_scope_scope_id_key;
ALTER TABLE tool_rate_limits ADD CONSTRAINT tool_rate_limits_tool_scope_tenant_key UNIQUE(tool_id, scope, scope_id, tenant_id);

-- Add tenant_id to sessions for defense-in-depth tenant isolation.
ALTER TABLE sessions ADD COLUMN tenant_id UUID REFERENCES tenants(id);
UPDATE sessions SET tenant_id = u.tenant_id FROM users u WHERE sessions.user_id = u.id;
ALTER TABLE sessions ALTER COLUMN tenant_id SET NOT NULL;
CREATE INDEX idx_sessions_tenant ON sessions(tenant_id);
