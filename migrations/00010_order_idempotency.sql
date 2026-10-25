-- +goose Up
ALTER TABLE orders ADD COLUMN request_fingerprint BYTEA;
ALTER TABLE orders ADD CONSTRAINT orders_request_fingerprint_length_check
    CHECK (request_fingerprint IS NULL OR octet_length(request_fingerprint) = 32);

-- +goose Down
-- Orders and tokens survive, but rollback removes replay guarantees.
ALTER TABLE orders DROP COLUMN request_fingerprint;
