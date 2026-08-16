-- Migration: Create stores table for Direktori Tokoh Titip Beli
-- Stores are admin-managed locations (tokoh/toko) with GPS coordinates.
-- The 'items' column is reserved for future use (list of products in a store).

CREATE TABLE IF NOT EXISTS stores (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL,
    address     TEXT,
    lat         DOUBLE PRECISION NOT NULL,
    lng         DOUBLE PRECISION NOT NULL,
    category    TEXT,
    description TEXT,
    image_url   TEXT,
    items       JSONB NOT NULL DEFAULT '[]',
    is_active   BOOLEAN NOT NULL DEFAULT true,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Index for filtering active stores (most queries use is_active = true)
CREATE INDEX IF NOT EXISTS idx_stores_is_active ON stores(is_active);

-- Index for proximity search via lat/lng (used in FindNearby)
-- PostGIS handles geometry separately; these raw lat/lng indexes help basic bbox filters
CREATE INDEX IF NOT EXISTS idx_stores_lat_lng ON stores(lat, lng);

-- GIN index for future items search
CREATE INDEX IF NOT EXISTS idx_stores_items_gin ON stores USING GIN (items);
