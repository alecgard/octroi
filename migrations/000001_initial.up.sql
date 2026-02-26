-- Consolidated schema for Octroi API gateway.

CREATE TABLE tools (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    description TEXT NOT NULL,
    endpoint TEXT NOT NULL,
    auth_type TEXT NOT NULL DEFAULT 'none',
    auth_config TEXT DEFAULT '{}',
    pricing_model TEXT,
    pricing_amount NUMERIC(12,6) DEFAULT 0,
    pricing_currency TEXT DEFAULT 'USD',
    rate_limit INT DEFAULT 0,
    budget_limit NUMERIC(12,6) DEFAULT 0,
    budget_window TEXT DEFAULT 'monthly',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    mode TEXT NOT NULL DEFAULT 'service',
    variables JSONB DEFAULT '{}',
    transport TEXT,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    log_bodies BOOLEAN NOT NULL DEFAULT true,
    timeout_ms INT NOT NULL DEFAULT 30000,
    max_retries INT NOT NULL DEFAULT 0,
    retry_backoff_ms INT NOT NULL DEFAULT 1000,
    webhook_url TEXT DEFAULT '',
    webhook_threshold_pct INT NOT NULL DEFAULT 80,
    cb_enabled BOOLEAN NOT NULL DEFAULT false,
    cb_error_threshold_pct INT NOT NULL DEFAULT 90,
    cb_window_seconds INT NOT NULL DEFAULT 120,
    cb_cooldown_seconds INT NOT NULL DEFAULT 60,
    archived_at TIMESTAMPTZ
);

CREATE TABLE agents (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    api_key_hash TEXT NOT NULL UNIQUE,
    api_key_prefix TEXT NOT NULL,
    team TEXT DEFAULT '',
    rate_limit INT DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    allowlist_mode BOOLEAN NOT NULL DEFAULT false,
    archived_at TIMESTAMPTZ
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
    agent_id UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    tool_id UUID NOT NULL REFERENCES tools(id) ON DELETE CASCADE,
    timestamp TIMESTAMPTZ NOT NULL DEFAULT now(),
    method TEXT NOT NULL,
    path TEXT NOT NULL,
    status_code INT NOT NULL,
    latency_ms BIGINT NOT NULL,
    request_size BIGINT DEFAULT 0,
    response_size BIGINT DEFAULT 0,
    success BOOLEAN NOT NULL,
    cost NUMERIC(12,6) DEFAULT 0,
    error TEXT DEFAULT '',
    cost_source TEXT NOT NULL DEFAULT 'flat',
    agent_name TEXT NOT NULL DEFAULT '',
    tool_name TEXT NOT NULL DEFAULT ''
);

CREATE INDEX idx_transactions_agent_id ON transactions(agent_id);
CREATE INDEX idx_transactions_tool_id ON transactions(tool_id);
CREATE INDEX idx_transactions_timestamp ON transactions(timestamp);
CREATE INDEX idx_transactions_agent_tool ON transactions(agent_id, tool_id);
CREATE INDEX idx_transactions_status_code ON transactions(status_code);
CREATE INDEX idx_transactions_latency_ms ON transactions(latency_ms);

CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    name TEXT NOT NULL DEFAULT '',
    teams JSONB NOT NULL DEFAULT '[]',
    role TEXT NOT NULL DEFAULT 'member',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    archived_at TIMESTAMPTZ
);

CREATE TABLE sessions (
    token_hash TEXT PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_sessions_user_id ON sessions(user_id);
CREATE INDEX idx_sessions_expires_at ON sessions(expires_at);

CREATE TABLE tool_rate_limits (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tool_id UUID NOT NULL REFERENCES tools(id) ON DELETE CASCADE,
    scope TEXT NOT NULL CHECK (scope IN ('team', 'agent')),
    scope_id TEXT NOT NULL,
    rate_limit INT NOT NULL CHECK (rate_limit > 0),
    UNIQUE(tool_id, scope, scope_id)
);

CREATE INDEX idx_tool_rate_limits_tool ON tool_rate_limits(tool_id);

CREATE TABLE agent_tool_permissions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_id UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    tool_id UUID NOT NULL REFERENCES tools(id) ON DELETE CASCADE,
    allowed BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    sub_tools TEXT[],
    UNIQUE(agent_id, tool_id)
);

CREATE TABLE request_bodies (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    transaction_id UUID NOT NULL,
    request_body BYTEA,
    response_body BYTEA,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_request_bodies_txn ON request_bodies(transaction_id);
CREATE INDEX idx_request_bodies_created ON request_bodies(created_at);

CREATE TABLE audit_log (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    timestamp TIMESTAMPTZ NOT NULL DEFAULT now(),
    user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    action TEXT NOT NULL,
    resource_type TEXT NOT NULL,
    resource_id TEXT NOT NULL,
    details JSONB DEFAULT '{}',
    ip TEXT DEFAULT '',
    request_id TEXT DEFAULT ''
);

CREATE INDEX idx_audit_log_ts ON audit_log(timestamp);
CREATE INDEX idx_audit_log_type ON audit_log(resource_type);
CREATE INDEX idx_audit_log_user ON audit_log(user_id);
