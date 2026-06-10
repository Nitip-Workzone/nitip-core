-- +goose Up
ALTER TABLE users ADD COLUMN device_id VARCHAR(255);

-- +goose Down
ALTER TABLE users DROP COLUMN device_id;
