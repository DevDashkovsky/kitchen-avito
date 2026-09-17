-- +goose Up
CREATE EXTENSION IF NOT EXISTS pgcrypto;

UPDATE establishments
SET accepts_orders = FALSE,
    api_key_hash = gen_random_bytes(32),
    updated_at = NOW()
WHERE api_key_hash = decode(
    '5f7b2ed74a9736a538a6fb5a5a1a76afdcdd0d177d7fc6e79c6b6843527d848d',
    'hex'
);

-- +goose Down
-- The retired public credential must not be restored automatically.
SELECT 1;
