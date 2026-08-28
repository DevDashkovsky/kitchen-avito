-- +goose Up
CREATE TABLE menu_categories (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    establishment_id BIGINT NOT NULL,
    name TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT menu_categories_establishment_fk
        FOREIGN KEY (establishment_id) REFERENCES establishments (id) ON DELETE RESTRICT,
    CONSTRAINT menu_categories_name_length_check
        CHECK (char_length(btrim(name)) BETWEEN 1 AND 100)
);

CREATE UNIQUE INDEX menu_categories_establishment_name_uidx
    ON menu_categories (establishment_id, lower(name));

-- +goose Down
DROP TABLE IF EXISTS menu_categories;
