-- +goose Up
-- +goose StatementBegin

-- P1 30 Juli: merchants PostGIS GIST + reviews + order_items + users runner live enhanced
-- Fixes remaining heavy queries after P2 28 Juli

-- Ensure PostGIS (idempotent)
CREATE EXTENSION IF NOT EXISTS postgis;

-- 1. Merchants geography column + GIST for ST_DWithin (was acos full scan)
ALTER TABLE merchants ADD COLUMN IF NOT EXISTS geom geography(Point, 4326);

UPDATE merchants
SET geom = ST_SetSRID(ST_MakePoint(longitude, latitude), 4326)::geography
WHERE geom IS NULL AND latitude IS NOT NULL AND longitude IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_merchants_geom_gist ON merchants USING GIST(geom);
CREATE INDEX IF NOT EXISTS idx_merchants_is_open ON merchants(is_open) WHERE is_open = true;
CREATE INDEX IF NOT EXISTS idx_merchants_owner_id ON merchants(owner_id);
CREATE INDEX IF NOT EXISTS idx_merchants_created_at ON merchants(created_at DESC);

-- Trigger auto-populate geom
CREATE OR REPLACE FUNCTION merchants_set_geom() RETURNS TRIGGER AS $$
BEGIN
    IF NEW.latitude IS NOT NULL AND NEW.longitude IS NOT NULL THEN
        NEW.geom := ST_SetSRID(ST_MakePoint(NEW.longitude, NEW.latitude), 4326)::geography;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_merchants_set_geom ON merchants;
CREATE TRIGGER trg_merchants_set_geom
    BEFORE INSERT OR UPDATE OF latitude, longitude
    ON merchants
    FOR EACH ROW EXECUTE FUNCTION merchants_set_geom();

-- 2. Reviews indexes for avg rating (was full scan)
CREATE INDEX IF NOT EXISTS idx_reviews_runner_id ON reviews(runner_id) WHERE runner_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_reviews_merchant_id ON reviews(merchant_id) WHERE merchant_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_reviews_requester_id ON reviews(requester_id) WHERE requester_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_reviews_order_id ON reviews(order_id);
CREATE INDEX IF NOT EXISTS idx_reviews_runner_rating ON reviews(runner_id, runner_rating) WHERE runner_rating IS NOT NULL;

-- 3. Order items order_id index (was seq scan)
CREATE INDEX IF NOT EXISTS idx_order_items_order_id ON order_items(order_id);
CREATE INDEX IF NOT EXISTS idx_menus_merchant_id ON menus(merchant_id);
CREATE INDEX IF NOT EXISTS idx_menus_is_available ON menus(merchant_id, is_available);

-- 4. Users enhanced indexes for FindNearbyRunners bounding box + FindAll limit
CREATE INDEX IF NOT EXISTS idx_users_role_created ON users(role, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_users_last_location ON users(last_lat, last_lng) WHERE role='runner' AND is_suspended=false;

-- 5. Wallet transactions optimized for summary single query
CREATE INDEX IF NOT EXISTS idx_wallet_tx_type_status ON wallet_transactions(wallet_id, type, status);

-- 6. Support tickets assignee for active ticket lookup
CREATE INDEX IF NOT EXISTS idx_support_tickets_cs_status ON support_tickets(assigned_cs_id, status) WHERE assigned_cs_id IS NOT NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS trg_merchants_set_geom ON merchants;
DROP FUNCTION IF EXISTS merchants_set_geom();
DROP INDEX IF EXISTS idx_merchants_geom_gist;
DROP INDEX IF EXISTS idx_merchants_is_open;
DROP INDEX IF EXISTS idx_merchants_owner_id;
DROP INDEX IF EXISTS idx_merchants_created_at;
DROP INDEX IF EXISTS idx_reviews_runner_id;
DROP INDEX IF EXISTS idx_reviews_merchant_id;
DROP INDEX IF EXISTS idx_reviews_requester_id;
DROP INDEX IF EXISTS idx_reviews_order_id;
DROP INDEX IF EXISTS idx_reviews_runner_rating;
DROP INDEX IF EXISTS idx_order_items_order_id;
DROP INDEX IF EXISTS idx_menus_merchant_id;
DROP INDEX IF EXISTS idx_menus_is_available;
DROP INDEX IF EXISTS idx_users_role_created;
DROP INDEX IF EXISTS idx_users_last_location;
DROP INDEX IF EXISTS idx_wallet_tx_type_status;
DROP INDEX IF EXISTS idx_support_tickets_cs_status;
-- Keep geom column for compatibility, just drop indexes above
-- +goose StatementEnd
