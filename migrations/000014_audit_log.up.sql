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
