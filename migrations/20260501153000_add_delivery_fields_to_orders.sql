-- +goose Up
-- +goose StatementBegin
ALTER TABLE orders 
    ADD COLUMN service_category VARCHAR(20) NOT NULL DEFAULT 'beli',
    ADD COLUMN receiver_name VARCHAR(255) NULL,
    ADD COLUMN receiver_phone VARCHAR(20) NULL,
    ADD COLUMN delivery_name VARCHAR(255) NULL,
    ADD COLUMN delivery_address TEXT NULL;

CREATE INDEX idx_orders_service_category ON orders(service_category);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_orders_service_category;
ALTER TABLE orders 
    DROP COLUMN service_category,
    DROP COLUMN receiver_name,
    DROP COLUMN receiver_phone,
    DROP COLUMN delivery_name,
    DROP COLUMN delivery_address;
-- +goose StatementEnd
