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

const finalizeDelivery = `
UPDATE deliveries
SET
    attempts_made = attempts_made + 1,

    status = CASE
        WHEN :outcome = 'succeeded' THEN 'delivered'
        WHEN :retry_at IS NOT NULL
             AND attempts_made + 1 < max_attempts THEN 'pending'
        ELSE 'dead_letter'
    END,

    next_attempt_at = CASE
        WHEN :outcome != 'succeeded'
             AND :retry_at IS NOT NULL
             AND attempts_made + 1 < max_attempts THEN :retry_at
        ELSE next_attempt_at
    END,

    delivered_at = CASE
        WHEN :outcome = 'succeeded' THEN :finished_at
        ELSE NULL
    END,

    last_error = CASE
        WHEN :outcome = 'succeeded' THEN NULL
        ELSE :error_message
    END,

    lease_token = NULL,
    lease_expires_at = NULL,
    updated_at = :now

WHERE id = :delivery_id
  AND status = 'in_progress'
  AND lease_token = :lease_token
  AND attempts_made < max_attempts

RETURNING attempts_made
`

const insertCompletedAttempt = `
INSERT INTO delivery_attempts (
    delivery_id,
    attempt_number,
    started_at,
    finished_at,
    outcome,
    response_status,
    error_message,
    duration_ms
) VALUES (
    :delivery_id,
    :attempt_number,
    :started_at,
    :finished_at,
    :outcome,
    :response_status,
    :error_message,
    :duration_ms
)
`
