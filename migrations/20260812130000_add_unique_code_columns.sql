-- +goose Up
-- +goose StatementBegin
ALTER TABLE orders ADD COLUMN unique_code INT NOT NULL DEFAULT 0;
ALTER TABLE wallet_transactions ADD COLUMN unique_code INT NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE orders DROP COLUMN unique_code;
ALTER TABLE wallet_transactions DROP COLUMN unique_code;
-- +goose StatementEnd
