-- +goose Up
-- +goose StatementBegin
ALTER TABLE users 
    ADD COLUMN is_suspended BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN suspended_reason TEXT;

CREATE INDEX idx_users_is_suspended ON users(is_suspended);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_users_is_suspended;
ALTER TABLE users 
    DROP COLUMN IF EXISTS is_suspended,
    DROP COLUMN IF EXISTS suspended_reason;
-- +goose StatementEnd
