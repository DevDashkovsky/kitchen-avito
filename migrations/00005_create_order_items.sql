-- +goose Up
CREATE TABLE order_items (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    order_id BIGINT NOT NULL,
    dish_id BIGINT NOT NULL,
    dish_name TEXT NOT NULL,
    unit_price_minor BIGINT NOT NULL,
    quantity INTEGER NOT NULL,
    CONSTRAINT order_items_order_fk
        FOREIGN KEY (order_id) REFERENCES orders (id) ON DELETE RESTRICT,
    CONSTRAINT order_items_dish_fk
        FOREIGN KEY (dish_id) REFERENCES dishes (id) ON DELETE RESTRICT,
    CONSTRAINT order_items_order_dish_unique
        UNIQUE (order_id, dish_id),
    CONSTRAINT order_items_dish_name_length_check
        CHECK (char_length(btrim(dish_name)) BETWEEN 1 AND 200),
    CONSTRAINT order_items_unit_price_nonnegative_check
        CHECK (unit_price_minor >= 0),
    CONSTRAINT order_items_quantity_positive_check
        CHECK (quantity > 0)
);

-- +goose Down
DROP TABLE IF EXISTS order_items;
