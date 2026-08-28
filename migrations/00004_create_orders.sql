-- +goose Up
CREATE TABLE orders (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    establishment_id BIGINT NOT NULL,
    tracking_token_hash BYTEA NOT NULL UNIQUE,
    status TEXT NOT NULL,
    customer_name TEXT NOT NULL,
    customer_phone TEXT NOT NULL,
    delivery_address TEXT NOT NULL,
    comment TEXT,
    total_minor BIGINT NOT NULL,
    rejection_reason TEXT,
    rejection_comment TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT orders_establishment_fk
        FOREIGN KEY (establishment_id) REFERENCES establishments (id) ON DELETE RESTRICT,
    CONSTRAINT orders_status_check
        CHECK (status IN (
            'pending_confirmation',
            'confirmed',
            'preparing',
            'ready_for_delivery',
            'delivering',
            'completed',
            'rejected',
            'cancelled'
        )),
    CONSTRAINT orders_customer_name_length_check
        CHECK (char_length(btrim(customer_name)) BETWEEN 1 AND 200),
    CONSTRAINT orders_customer_phone_length_check
        CHECK (char_length(btrim(customer_phone)) BETWEEN 5 AND 32),
    CONSTRAINT orders_delivery_address_length_check
        CHECK (char_length(btrim(delivery_address)) BETWEEN 1 AND 500),
    CONSTRAINT orders_comment_length_check
        CHECK (comment IS NULL OR char_length(comment) <= 2000),
    CONSTRAINT orders_total_nonnegative_check
        CHECK (total_minor >= 0),
    CONSTRAINT orders_rejection_reason_check
        CHECK (
            rejection_reason IS NULL
            OR rejection_reason IN (
                'out_of_stock',
                'overloaded',
                'delivery_unavailable',
                'other',
                'confirmation_timeout'
            )
        ),
    CONSTRAINT orders_rejection_comment_length_check
        CHECK (rejection_comment IS NULL OR char_length(rejection_comment) <= 2000),
    CONSTRAINT orders_rejection_fields_check
        CHECK (
            (status = 'rejected' AND rejection_reason IS NOT NULL)
            OR (
                status <> 'rejected'
                AND rejection_reason IS NULL
                AND rejection_comment IS NULL
            )
        ),
    CONSTRAINT orders_other_rejection_comment_check
        CHECK (
            rejection_reason IS DISTINCT FROM 'other'
            OR NULLIF(btrim(rejection_comment), '') IS NOT NULL
        )
);

CREATE INDEX orders_establishment_status_created_at_idx
    ON orders (establishment_id, status, created_at);

CREATE INDEX orders_pending_confirmation_created_at_idx
    ON orders (created_at)
    WHERE status = 'pending_confirmation';

-- +goose Down
DROP TABLE IF EXISTS orders;
