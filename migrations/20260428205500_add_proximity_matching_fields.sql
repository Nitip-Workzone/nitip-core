-- +goose Up
-- +goose StatementBegin
ALTER TABLE users ADD COLUMN is_accepting_orders BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE orders ADD COLUMN pickup_name TEXT;
ALTER TABLE orders ADD COLUMN pickup_address TEXT;
ALTER TABLE orders ADD COLUMN distance_km DOUBLE PRECISION DEFAULT 0;

CREATE INDEX idx_orders_distance_km ON orders(distance_km);
CREATE INDEX idx_users_is_accepting_orders ON users(is_accepting_orders);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_users_is_accepting_orders;
DROP INDEX IF EXISTS idx_orders_distance_km;
ALTER TABLE orders DROP COLUMN IF EXISTS distance_km;
ALTER TABLE orders DROP COLUMN IF EXISTS pickup_address;
ALTER TABLE orders DROP COLUMN IF EXISTS pickup_name;
ALTER TABLE users DROP COLUMN IF EXISTS is_accepting_orders;
-- +goose StatementEnd
