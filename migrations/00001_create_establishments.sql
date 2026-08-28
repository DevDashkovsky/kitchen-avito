-- +goose Up
CREATE TABLE establishments (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name TEXT NOT NULL,
    accepts_orders BOOLEAN NOT NULL DEFAULT FALSE,
    api_key_hash BYTEA NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT establishments_name_length_check
        CHECK (char_length(btrim(name)) BETWEEN 1 AND 200)
);

-- +goose Down
DROP TABLE IF EXISTS establishments;
