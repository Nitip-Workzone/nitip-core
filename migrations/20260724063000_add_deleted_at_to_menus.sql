-- +goose Up
-- +goose StatementBegin
ALTER TABLE menus ADD COLUMN deleted_at TIMESTAMP WITH TIME ZONE;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE menus DROP COLUMN IF EXISTS deleted_at;
-- +goose StatementEnd
