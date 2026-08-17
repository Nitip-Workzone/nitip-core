-- +goose Up
-- +goose StatementBegin
ALTER TABLE orders ADD COLUMN IF NOT EXISTS merchant_fee NUMERIC NOT NULL DEFAULT 0;
ALTER TABLE orders ADD COLUMN IF NOT EXISTS merchant_fee_tier SMALLINT NOT NULL DEFAULT 0;
ALTER TABLE orders ADD COLUMN IF NOT EXISTS food_amount_original NUMERIC NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE orders DROP COLUMN IF EXISTS food_amount_original;
ALTER TABLE orders DROP COLUMN IF EXISTS merchant_fee_tier;
ALTER TABLE orders DROP COLUMN IF EXISTS merchant_fee;
-- +goose StatementEnd
