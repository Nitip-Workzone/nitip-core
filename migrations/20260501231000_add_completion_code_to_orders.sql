-- +goose Up
-- +goose StatementBegin
ALTER TABLE orders ADD COLUMN completion_code VARCHAR(10) NULL;
CREATE INDEX idx_orders_completion_code ON orders(completion_code);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_orders_completion_code;
ALTER TABLE orders DROP COLUMN IF EXISTS completion_code;
-- +goose StatementEnd
