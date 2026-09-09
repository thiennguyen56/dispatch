package sqlite

const insertDelivery = `
INSERT INTO deliveries (
    id,
    target_url,
    payload,
    headers_json,
    status,
    attempts_made,
    max_attempts,
    next_attempt_at,
    lease_token,
    lease_expires_at,
    last_error,
    idempotency_key,
    created_at,
    updated_at,
    delivered_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

const getDelivery = `
SELECT
    id,
    target_url,
    payload,
    headers_json,
    status,
    attempts_made,
    max_attempts,
    next_attempt_at,
    lease_token,
    lease_expires_at,
    last_error,
    idempotency_key,
    created_at,
    updated_at,
    delivered_at
FROM deliveries
WHERE id = ?
`
