DROP INDEX IF EXISTS idx_audit_log_tenant;
DROP INDEX IF EXISTS idx_transactions_tenant;
DROP INDEX IF EXISTS idx_tools_tenant;
DROP INDEX IF EXISTS idx_agents_tenant;

ALTER TABLE users DROP CONSTRAINT IF EXISTS users_tenant_email_key;
ALTER TABLE users ADD CONSTRAINT users_email_key UNIQUE(email);

ALTER TABLE request_bodies DROP COLUMN IF EXISTS tenant_id;
ALTER TABLE audit_log DROP COLUMN IF EXISTS tenant_id;
ALTER TABLE tool_rate_limits DROP COLUMN IF EXISTS tenant_id;
ALTER TABLE agent_tool_permissions DROP COLUMN IF EXISTS tenant_id;
ALTER TABLE agent_tool_budgets DROP COLUMN IF EXISTS tenant_id;
ALTER TABLE transactions DROP COLUMN IF EXISTS tenant_id;
ALTER TABLE users DROP COLUMN IF EXISTS tenant_id;
ALTER TABLE tools DROP COLUMN IF EXISTS tenant_id;
ALTER TABLE agents DROP COLUMN IF EXISTS tenant_id;

DELETE FROM tenants WHERE id = '00000000-0000-0000-0000-000000000001';
