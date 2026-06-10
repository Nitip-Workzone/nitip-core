-- +goose Up
ALTER TABLE users ADD COLUMN home_lat DOUBLE PRECISION;
ALTER TABLE users ADD COLUMN home_lng DOUBLE PRECISION;
ALTER TABLE users ADD COLUMN home_address TEXT;

-- +goose Down
ALTER TABLE users DROP COLUMN home_lat;
ALTER TABLE users DROP COLUMN home_lng;
ALTER TABLE users DROP COLUMN home_address;
