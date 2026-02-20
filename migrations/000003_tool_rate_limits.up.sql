CREATE TABLE tool_rate_limits (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tool_id UUID NOT NULL REFERENCES tools(id) ON DELETE CASCADE,
    scope TEXT NOT NULL CHECK (scope IN ('team', 'agent')),
    scope_id TEXT NOT NULL,
    rate_limit INT NOT NULL CHECK (rate_limit > 0),
    UNIQUE(tool_id, scope, scope_id)
);

CREATE INDEX idx_tool_rate_limits_tool ON tool_rate_limits(tool_id);
