-- +goose Up
-- +goose StatementBegin
-- Add capacity and vehicle fields to trips table
ALTER TABLE trips ADD COLUMN max_weight_kg DOUBLE PRECISION NOT NULL DEFAULT 0;
ALTER TABLE trips ADD COLUMN available_weight_kg DOUBLE PRECISION NOT NULL DEFAULT 0;
ALTER TABLE trips ADD COLUMN max_volume_liters DOUBLE PRECISION NOT NULL DEFAULT 0;
ALTER TABLE trips ADD COLUMN available_volume_liters DOUBLE PRECISION NOT NULL DEFAULT 0;
ALTER TABLE trips ADD COLUMN vehicle_type VARCHAR(50) NOT NULL DEFAULT 'motorcycle';

-- Add capacity and trip tracking to orders table
ALTER TABLE orders ADD COLUMN weight_kg DOUBLE PRECISION NOT NULL DEFAULT 0;
ALTER TABLE orders ADD COLUMN volume_liters DOUBLE PRECISION NOT NULL DEFAULT 0;
ALTER TABLE orders ADD COLUMN trip_id UUID REFERENCES trips(id) ON DELETE SET NULL;

-- Index for trip_id in orders
CREATE INDEX idx_orders_trip_id ON orders(trip_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_orders_trip_id;
ALTER TABLE orders DROP COLUMN IF EXISTS weight_kg;
ALTER TABLE orders DROP COLUMN IF EXISTS volume_liters;
ALTER TABLE orders DROP COLUMN IF EXISTS trip_id;

ALTER TABLE trips DROP COLUMN IF EXISTS max_weight_kg;
ALTER TABLE trips DROP COLUMN IF EXISTS available_weight_kg;
ALTER TABLE trips DROP COLUMN IF EXISTS max_volume_liters;
ALTER TABLE trips DROP COLUMN IF EXISTS available_volume_liters;
ALTER TABLE trips DROP COLUMN IF EXISTS vehicle_type;
-- +goose StatementEnd
