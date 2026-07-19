-- +goose Up
-- +goose StatementBegin
ALTER TABLE orders ADD COLUMN payment_source VARCHAR(20) NOT NULL DEFAULT 'wallet';
CREATE INDEX idx_orders_payment_status ON orders(payment_status);
CREATE INDEX idx_orders_payment_method ON orders(payment_method);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_orders_payment_method;
DROP INDEX IF EXISTS idx_orders_payment_status;
ALTER TABLE orders DROP COLUMN IF EXISTS payment_source;
-- +goose StatementEnd
