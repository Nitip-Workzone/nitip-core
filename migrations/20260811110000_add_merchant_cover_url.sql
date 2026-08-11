-- +goose Up
-- SQL in this section is executed when the migration is applied.
ALTER TABLE merchants ADD COLUMN cover_url VARCHAR(255) NULL;

-- +goose Down
-- SQL in this section is executed when the migration is rolled back.
ALTER TABLE merchants DROP COLUMN cover_url;
