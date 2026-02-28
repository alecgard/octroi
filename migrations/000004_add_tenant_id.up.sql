-- Create a default tenant for existing data.
INSERT INTO tenants (id, name, slug, plan)
VALUES ('00000000-0000-0000-0000-000000000001', 'Default', 'default', 'free')
ON CONFLICT DO NOTHING;

-- Add tenant_id to all tenant-scoped tables.
ALTER TABLE agents ADD COLUMN tenant_id UUID REFERENCES tenants(id);
ALTER TABLE tools ADD COLUMN tenant_id UUID REFERENCES tenants(id);
ALTER TABLE users ADD COLUMN tenant_id UUID REFERENCES tenants(id);
ALTER TABLE transactions ADD COLUMN tenant_id UUID REFERENCES tenants(id);
ALTER TABLE agent_tool_budgets ADD COLUMN tenant_id UUID REFERENCES tenants(id);
ALTER TABLE agent_tool_permissions ADD COLUMN tenant_id UUID REFERENCES tenants(id);
ALTER TABLE tool_rate_limits ADD COLUMN tenant_id UUID REFERENCES tenants(id);
ALTER TABLE audit_log ADD COLUMN tenant_id UUID REFERENCES tenants(id);
ALTER TABLE request_bodies ADD COLUMN tenant_id UUID REFERENCES tenants(id);

-- Backfill existing rows with the default tenant.
UPDATE agents SET tenant_id = '00000000-0000-0000-0000-000000000001' WHERE tenant_id IS NULL;
UPDATE tools SET tenant_id = '00000000-0000-0000-0000-000000000001' WHERE tenant_id IS NULL;
UPDATE users SET tenant_id = '00000000-0000-0000-0000-000000000001' WHERE tenant_id IS NULL;
UPDATE transactions SET tenant_id = '00000000-0000-0000-0000-000000000001' WHERE tenant_id IS NULL;
UPDATE agent_tool_budgets SET tenant_id = '00000000-0000-0000-0000-000000000001' WHERE tenant_id IS NULL;
UPDATE agent_tool_permissions SET tenant_id = '00000000-0000-0000-0000-000000000001' WHERE tenant_id IS NULL;
UPDATE tool_rate_limits SET tenant_id = '00000000-0000-0000-0000-000000000001' WHERE tenant_id IS NULL;
UPDATE audit_log SET tenant_id = '00000000-0000-0000-0000-000000000001' WHERE tenant_id IS NULL;
UPDATE request_bodies SET tenant_id = '00000000-0000-0000-0000-000000000001' WHERE tenant_id IS NULL;

-- Make tenant_id NOT NULL now that all rows are backfilled.
ALTER TABLE agents ALTER COLUMN tenant_id SET NOT NULL;
ALTER TABLE tools ALTER COLUMN tenant_id SET NOT NULL;
ALTER TABLE users ALTER COLUMN tenant_id SET NOT NULL;
ALTER TABLE transactions ALTER COLUMN tenant_id SET NOT NULL;
ALTER TABLE agent_tool_budgets ALTER COLUMN tenant_id SET NOT NULL;
ALTER TABLE agent_tool_permissions ALTER COLUMN tenant_id SET NOT NULL;
ALTER TABLE tool_rate_limits ALTER COLUMN tenant_id SET NOT NULL;
ALTER TABLE audit_log ALTER COLUMN tenant_id SET NOT NULL;
ALTER TABLE request_bodies ALTER COLUMN tenant_id SET NOT NULL;

-- Update unique constraints for tenant scoping.
-- Users: email must be unique per tenant (not globally).
ALTER TABLE users DROP CONSTRAINT users_email_key;
ALTER TABLE users ADD CONSTRAINT users_tenant_email_key UNIQUE(tenant_id, email);

-- agents.api_key_hash stays globally unique (agent auth is hash-based, tenant unknown at lookup).

-- Add tenant-scoped indexes.
CREATE INDEX idx_agents_tenant ON agents(tenant_id) WHERE archived_at IS NULL;
CREATE INDEX idx_tools_tenant ON tools(tenant_id) WHERE archived_at IS NULL;
CREATE INDEX idx_transactions_tenant ON transactions(tenant_id, timestamp DESC);
CREATE INDEX idx_audit_log_tenant ON audit_log(tenant_id, timestamp DESC);
