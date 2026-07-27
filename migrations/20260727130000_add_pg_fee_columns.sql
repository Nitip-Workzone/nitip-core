-- +goose Up
ALTER TABLE orders ADD COLUMN pg_fee DECIMAL(12, 2) NOT NULL DEFAULT 0;
ALTER TABLE wallet_transactions ADD COLUMN pg_fee DECIMAL(12, 2) NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE orders DROP COLUMN pg_fee;
ALTER TABLE wallet_transactions DROP COLUMN pg_fee;
