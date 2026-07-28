-- +goose Up
-- +goose StatementBegin

-- Ensure PostGIS extension (already created earlier, but idempotent)
CREATE EXTENSION IF NOT EXISTS postgis;

-- Add geography columns for fast ST_DWithin queries (replaces fmt.Sprintf haversine)
ALTER TABLE orders
    ADD COLUMN IF NOT EXISTS pickup_geom geography(Point, 4326),
    ADD COLUMN IF NOT EXISTS delivery_geom geography(Point, 4326);

-- Backfill existing rows
UPDATE orders
SET pickup_geom = ST_SetSRID(ST_MakePoint(pickup_lng, pickup_lat), 4326)::geography
WHERE pickup_geom IS NULL AND pickup_lat IS NOT NULL AND pickup_lng IS NOT NULL;

UPDATE orders
SET delivery_geom = ST_SetSRID(ST_MakePoint(delivery_lng, delivery_lat), 4326)::geography
WHERE delivery_geom IS NULL AND delivery_lat IS NOT NULL AND delivery_lng IS NOT NULL;

-- GIST index for ST_DWithin
CREATE INDEX IF NOT EXISTS idx_orders_pickup_geom_gist ON orders USING GIST(pickup_geom);
CREATE INDEX IF NOT EXISTS idx_orders_delivery_geom_gist ON orders USING GIST(delivery_geom);

-- Composite for pool filtering (status + payment + created + geom will use bitmap)
CREATE INDEX IF NOT EXISTS idx_orders_pool_filter ON orders(status, payment_status, created_at DESC) WHERE status IN ('pending','merchant_accepted','accepted','cooking','ready');

-- Trigger to auto-populate geom on insert/update (so app code doesn't have to)
CREATE OR REPLACE FUNCTION orders_set_geom() RETURNS TRIGGER AS $$
BEGIN
    IF NEW.pickup_lat IS NOT NULL AND NEW.pickup_lng IS NOT NULL THEN
        NEW.pickup_geom := ST_SetSRID(ST_MakePoint(NEW.pickup_lng, NEW.pickup_lat), 4326)::geography;
    END IF;
    IF NEW.delivery_lat IS NOT NULL AND NEW.delivery_lng IS NOT NULL THEN
        NEW.delivery_geom := ST_SetSRID(ST_MakePoint(NEW.delivery_lng, NEW.delivery_lat), 4326)::geography;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_orders_set_geom ON orders;
CREATE TRIGGER trg_orders_set_geom
    BEFORE INSERT OR UPDATE OF pickup_lat, pickup_lng, delivery_lat, delivery_lng
    ON orders
    FOR EACH ROW EXECUTE FUNCTION orders_set_geom();

-- Pool metrics table for observability without Grafana (uses existing Postgres only)
CREATE TABLE IF NOT EXISTS pool_metrics (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    event_type TEXT NOT NULL, -- order_created, claimed, cancelled, expired, broadcast, sse_connect, sse_disconnect
    order_id UUID,
    cell_key TEXT,
    runner_count INT DEFAULT 0,
    latency_ms INT DEFAULT 0,
    metadata JSONB DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_pool_metrics_created ON pool_metrics(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_pool_metrics_type ON pool_metrics(event_type);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS trg_orders_set_geom ON orders;
DROP FUNCTION IF EXISTS orders_set_geom();
DROP TABLE IF EXISTS pool_metrics;
DROP INDEX IF EXISTS idx_orders_pool_filter;
DROP INDEX IF EXISTS idx_orders_delivery_geom_gist;
DROP INDEX IF EXISTS idx_orders_pickup_geom_gist;
ALTER TABLE orders DROP COLUMN IF EXISTS delivery_geom;
ALTER TABLE orders DROP COLUMN IF EXISTS pickup_geom;
-- +goose StatementEnd
