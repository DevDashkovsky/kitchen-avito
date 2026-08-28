-- +goose Up
CREATE TABLE dishes (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    category_id BIGINT NOT NULL,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    price_minor BIGINT NOT NULL,
    is_available BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT dishes_category_fk
        FOREIGN KEY (category_id) REFERENCES menu_categories (id) ON DELETE RESTRICT,
    CONSTRAINT dishes_name_length_check
        CHECK (char_length(btrim(name)) BETWEEN 1 AND 200),
    CONSTRAINT dishes_description_length_check
        CHECK (char_length(description) <= 2000),
    CONSTRAINT dishes_price_nonnegative_check
        CHECK (price_minor >= 0)
);

CREATE INDEX dishes_category_id_idx
    ON dishes (category_id);

-- +goose Down
DROP TABLE IF EXISTS dishes;
