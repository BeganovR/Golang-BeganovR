-- +goose Up
CREATE TABLE idempotency_keys (
    key          UUID PRIMARY KEY,
    request_hash BYTEA NOT NULL,
    trip_id      UUID NOT NULL REFERENCES trips(id) ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idempotency_keys_created_at_idx ON idempotency_keys (created_at);

CREATE INDEX idempotency_keys_trip_id_idx ON idempotency_keys (trip_id);

-- +goose Down
DROP TABLE IF EXISTS idempotency_keys;
