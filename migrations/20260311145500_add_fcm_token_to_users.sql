-- +goose Up
-- +goose StatementBegin
ALTER TABLE users ADD COLUMN fcm_token VARCHAR(255) NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE users DROP COLUMN IF EXISTS fcm_token;
-- +goose StatementEnd
