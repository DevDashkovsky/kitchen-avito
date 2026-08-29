-- +goose Up
ALTER TABLE order_items
    DROP CONSTRAINT order_items_quantity_positive_check,
    ADD CONSTRAINT order_items_quantity_range_check
        CHECK (quantity BETWEEN 1 AND 100) NOT VALID;

-- +goose Down
ALTER TABLE order_items
    DROP CONSTRAINT order_items_quantity_range_check,
    ADD CONSTRAINT order_items_quantity_positive_check
        CHECK (quantity > 0);
