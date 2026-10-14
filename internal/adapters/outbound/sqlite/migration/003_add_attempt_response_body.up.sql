ALTER TABLE delivery_attempts
    ADD COLUMN response_body TEXT;

ALTER TABLE delivery_attempts
    ADD COLUMN response_body_truncated INTEGER NOT NULL DEFAULT 0
    CHECK (response_body_truncated IN (0, 1));
