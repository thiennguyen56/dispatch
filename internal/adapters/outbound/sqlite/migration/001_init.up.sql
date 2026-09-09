PRAGMA foreign_keys = ON;

CREATE TABLE deliveries (
    id                TEXT PRIMARY KEY,
    target_url        TEXT NOT NULL,
    payload           BLOB NOT NULL,
    headers_json      TEXT NOT NULL DEFAULT '{}',

    status            TEXT NOT NULL
                      CHECK (status IN ('pending', 'in_progress', 'delivered', 'dead_letter')),
    attempts_made     INTEGER NOT NULL DEFAULT 0 CHECK (attempts_made >= 0),
    max_attempts      INTEGER NOT NULL DEFAULT 8 CHECK (max_attempts > 0),
    next_attempt_at   TEXT NOT NULL,

    lease_token       TEXT,
    lease_expires_at  TEXT,
    last_error        TEXT,

    idempotency_key   TEXT,
    created_at        TEXT NOT NULL,
    updated_at        TEXT NOT NULL,
    delivered_at      TEXT
);

CREATE UNIQUE INDEX deliveries_idempotency_key_idx
    ON deliveries (idempotency_key)
    WHERE idempotency_key IS NOT NULL;

CREATE INDEX deliveries_ready_idx
    ON deliveries (next_attempt_at, created_at)
    WHERE status = 'pending';

CREATE TABLE delivery_attempts (
    id                INTEGER PRIMARY KEY,
    delivery_id       TEXT NOT NULL REFERENCES deliveries(id),
    attempt_number    INTEGER NOT NULL CHECK (attempt_number > 0),

    started_at        TEXT NOT NULL,
    finished_at       TEXT,
    outcome           TEXT CHECK (outcome IN ('succeeded', 'failed', 'timed_out')),
    response_status   INTEGER CHECK (response_status BETWEEN 100 AND 599),
    error_message     TEXT,
    duration_ms       INTEGER CHECK (duration_ms >= 0),

    UNIQUE (delivery_id, attempt_number)
);

CREATE INDEX delivery_attempts_delivery_idx
    ON delivery_attempts (delivery_id, attempt_number DESC);
