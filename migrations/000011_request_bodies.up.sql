CREATE TABLE request_bodies (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    transaction_id UUID NOT NULL REFERENCES transactions(id) ON DELETE CASCADE,
    request_body  BYTEA,
    response_body BYTEA,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_request_bodies_txn ON request_bodies(transaction_id);
CREATE INDEX idx_request_bodies_created ON request_bodies(created_at);

ALTER TABLE tools ADD COLUMN log_bodies BOOLEAN NOT NULL DEFAULT false;

CREATE INDEX idx_transactions_status_code ON transactions(status_code);
CREATE INDEX idx_transactions_latency_ms ON transactions(latency_ms);
