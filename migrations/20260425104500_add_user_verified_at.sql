-- +goose Up
-- +goose StatementBegin
ALTER TABLE users ADD COLUMN verified_at TIMESTAMP;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE users DROP COLUMN verified_at;
-- +goose StatementEnd
