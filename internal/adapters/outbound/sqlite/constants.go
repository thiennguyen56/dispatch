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

const claimNextDelivery = `
UPDATE deliveries
SET
	status = 'in_progress',
	lease_token = :lease_token,
	lease_expires_at = :lease_expires_at,
	updated_at = :now
WHERE id = (
	SELECT id
	FROM deliveries
	WHERE attempts_made < max_attempts
      AND (
          (
              status = 'pending'
              AND next_attempt_at <= :now
          )
          OR
          (
              status = 'in_progress'
              AND lease_expires_at <= :now
          )
      )
    ORDER BY
        CASE
            WHEN status = 'pending' THEN next_attempt_at
            ELSE lease_expires_at
        END ASC,
        created_at ASC,
        id ASC
    LIMIT 1
)
RETURNING
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
    delivered_at;
`
