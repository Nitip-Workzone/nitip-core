-- +goose Up
-- +goose StatementBegin
ALTER TABLE merchants ADD COLUMN deleted_at TIMESTAMP WITH TIME ZONE;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE merchants DROP COLUMN IF EXISTS deleted_at;
-- +goose StatementEnd
