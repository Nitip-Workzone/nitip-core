-- +goose Up
-- +goose StatementBegin
ALTER TABLE users
ADD COLUMN totp_secret VARCHAR,
ADD COLUMN totp_enabled BOOLEAN NOT NULL DEFAULT false;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE users
DROP COLUMN totp_secret,
DROP COLUMN totp_enabled;
-- +goose StatementEnd
