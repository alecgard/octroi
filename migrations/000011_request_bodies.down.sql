DROP INDEX IF EXISTS idx_transactions_latency_ms;
DROP INDEX IF EXISTS idx_transactions_status_code;
ALTER TABLE tools DROP COLUMN IF EXISTS log_bodies;
DROP TABLE IF EXISTS request_bodies;
