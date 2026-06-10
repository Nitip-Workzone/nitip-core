-- +goose Up
ALTER TABLE users ADD COLUMN pin VARCHAR(255);

-- +goose Down
ALTER TABLE users DROP COLUMN pin;
