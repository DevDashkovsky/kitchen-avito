-- +goose Up
ALTER TABLE order_items
    VALIDATE CONSTRAINT order_items_quantity_range_check;

-- +goose Down
-- Validation only changes constraint metadata and is intentionally irreversible.
SELECT 1;
