-- +goose Up
-- +goose StatementBegin
ALTER TABLE orders ADD COLUMN qris_data TEXT;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE orders DROP COLUMN IF EXISTS qris_data;
-- +goose StatementEnd
