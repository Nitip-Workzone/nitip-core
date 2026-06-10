-- +goose Up
-- +goose StatementBegin
-- Add indexes for spatial search on orders table
CREATE INDEX idx_orders_pickup_location ON orders(pickup_lat, pickup_lng);
CREATE INDEX idx_orders_delivery_location ON orders(delivery_lat, delivery_lng);

-- Add composite index for common status filtering
CREATE INDEX idx_orders_status_created ON orders(status, created_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_orders_pickup_location;
DROP INDEX IF EXISTS idx_orders_delivery_location;
DROP INDEX IF EXISTS idx_orders_status_created;
-- +goose StatementEnd
