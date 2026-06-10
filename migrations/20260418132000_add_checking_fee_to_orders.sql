-- +goose Up
-- +goose StatementBegin
ALTER TABLE orders ADD COLUMN IF NOT EXISTS checking_fee NUMERIC NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE orders DROP COLUMN IF EXISTS checking_fee;
-- +goose StatementEnd
