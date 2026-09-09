DROP INDEX IF EXISTS delivery_attempts_delivery_idx;
DROP TABLE IF EXISTS delivery_attempts;

DROP INDEX IF EXISTS deliveries_ready_idx;
DROP INDEX IF EXISTS deliveries_idempotency_key_idx;
DROP TABLE IF EXISTS deliveries;
