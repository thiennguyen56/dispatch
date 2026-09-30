-- The repository has always written UTC RFC3339Nano strings ending in Z.
-- Pad fractions to nine digits without SQLite date functions, which would
-- lose nanosecond precision. NULL optional timestamps remain NULL.
UPDATE deliveries
SET next_attempt_at = substr(next_attempt_at, 1, 19) || '.' ||
        substr(replace(substr(next_attempt_at, 21), 'Z', '') || '000000000', 1, 9) || 'Z',
    lease_expires_at = substr(lease_expires_at, 1, 19) || '.' ||
        substr(replace(substr(lease_expires_at, 21), 'Z', '') || '000000000', 1, 9) || 'Z',
    created_at = substr(created_at, 1, 19) || '.' ||
        substr(replace(substr(created_at, 21), 'Z', '') || '000000000', 1, 9) || 'Z',
    updated_at = substr(updated_at, 1, 19) || '.' ||
        substr(replace(substr(updated_at, 21), 'Z', '') || '000000000', 1, 9) || 'Z',
    delivered_at = substr(delivered_at, 1, 19) || '.' ||
        substr(replace(substr(delivered_at, 21), 'Z', '') || '000000000', 1, 9) || 'Z';

UPDATE delivery_attempts
SET started_at = substr(started_at, 1, 19) || '.' ||
        substr(replace(substr(started_at, 21), 'Z', '') || '000000000', 1, 9) || 'Z',
    finished_at = substr(finished_at, 1, 19) || '.' ||
        substr(replace(substr(finished_at, 21), 'Z', '') || '000000000', 1, 9) || 'Z';
