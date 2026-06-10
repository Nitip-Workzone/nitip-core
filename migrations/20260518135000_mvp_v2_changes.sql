-- +goose Up
-- +goose StatementBegin
ALTER TABLE users ADD COLUMN whatsapp_number VARCHAR(20) NOT NULL DEFAULT '';
ALTER TABLE orders ADD COLUMN cod_handling_fee DECIMAL(12,2) NOT NULL DEFAULT 0.00;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE orders DROP COLUMN IF EXISTS cod_handling_fee;
ALTER TABLE users DROP COLUMN IF EXISTS whatsapp_number;
-- +goose StatementEnd
