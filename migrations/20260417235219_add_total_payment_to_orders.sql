-- +goose Up
-- +goose StatementBegin
ALTER TABLE orders ADD COLUMN total_payment DECIMAL(15,2) NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE orders DROP COLUMN total_payment;
-- +goose StatementEnd
