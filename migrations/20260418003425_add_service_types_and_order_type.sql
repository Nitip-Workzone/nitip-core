-- +goose Up
-- +goose StatementBegin
ALTER TABLE orders ADD COLUMN order_type VARCHAR(20) NOT NULL DEFAULT 'regular';
ALTER TABLE trips ADD COLUMN allowed_service_types TEXT[] NOT NULL DEFAULT '{instant,regular}';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE trips DROP COLUMN allowed_service_types;
ALTER TABLE orders DROP COLUMN order_type;
-- +goose StatementEnd
