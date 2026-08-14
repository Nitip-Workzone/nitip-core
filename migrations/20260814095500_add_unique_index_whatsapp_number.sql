-- +goose Up
-- +goose StatementBegin
CREATE UNIQUE INDEX idx_users_unique_whatsapp ON users(whatsapp_number) WHERE deleted_at IS NULL AND whatsapp_number != '';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_users_unique_whatsapp;
-- +goose StatementEnd
