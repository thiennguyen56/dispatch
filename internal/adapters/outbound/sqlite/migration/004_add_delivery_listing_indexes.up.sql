CREATE INDEX deliveries_created_id_idx
    ON deliveries (created_at DESC, id DESC);

CREATE INDEX deliveries_status_created_id_idx
    ON deliveries (status, created_at DESC, id DESC);
