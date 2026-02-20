CREATE TABLE tools (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    description TEXT NOT NULL,
    endpoint TEXT NOT NULL,
    auth_type TEXT NOT NULL DEFAULT 'none',
    auth_config JSONB DEFAULT '{}',
    pricing_model TEXT,
    pricing_amount NUMERIC(12,6) DEFAULT 0,
    pricing_currency TEXT DEFAULT 'USD',
    rate_limit INT DEFAULT 0,
    budget_limit NUMERIC(12,6) DEFAULT 0,
    budget_window TEXT DEFAULT 'monthly',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE agents (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    api_key_hash TEXT NOT NULL UNIQUE,
    api_key_prefix TEXT NOT NULL,
    team TEXT DEFAULT '',
    rate_limit INT DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE agent_tool_budgets (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_id UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    tool_id UUID NOT NULL REFERENCES tools(id) ON DELETE CASCADE,
    daily_limit NUMERIC(12,6) DEFAULT 0,
    monthly_limit NUMERIC(12,6) DEFAULT 0,
    UNIQUE(agent_id, tool_id)
);

CREATE TABLE transactions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_id UUID NOT NULL REFERENCES agents(id),
    tool_id UUID NOT NULL REFERENCES tools(id),
    timestamp TIMESTAMPTZ NOT NULL DEFAULT now(),
    method TEXT NOT NULL,
    path TEXT NOT NULL,
    status_code INT NOT NULL,
    latency_ms BIGINT NOT NULL,
    request_size BIGINT DEFAULT 0,
    response_size BIGINT DEFAULT 0,
    success BOOLEAN NOT NULL,
    cost NUMERIC(12,6) DEFAULT 0,
    error TEXT DEFAULT ''
);

CREATE INDEX idx_transactions_agent_id ON transactions(agent_id);
CREATE INDEX idx_transactions_tool_id ON transactions(tool_id);
CREATE INDEX idx_transactions_timestamp ON transactions(timestamp);
CREATE INDEX idx_transactions_agent_tool ON transactions(agent_id, tool_id);
