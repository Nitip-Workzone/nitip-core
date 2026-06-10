-- +goose Up
-- +goose StatementBegin

-- 1. Modify users table for Jastip-Lite (Add Roles, Geolocation, Trust Score)
ALTER TABLE users 
    ADD COLUMN role VARCHAR(20) NOT NULL DEFAULT 'requester',
    ADD COLUMN trust_score INT NOT NULL DEFAULT 0,
    ADD COLUMN is_verified BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN last_lat DOUBLE PRECISION,
    ADD COLUMN last_lng DOUBLE PRECISION;

CREATE INDEX idx_users_role ON users(role);
CREATE INDEX idx_users_last_location ON users(last_lat, last_lng);

-- 2. Create Configs table for dynamic settings
CREATE TABLE configs (
    key VARCHAR(100) PRIMARY KEY,
    value TEXT NOT NULL,
    description TEXT,
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

-- Insert default configs
INSERT INTO configs (key, value, description) VALUES
('max_search_radius_km', '5', 'Batas maksimal pencarian Mitra terdekat'),
('base_delivery_fee', '10000', 'Biaya pengantaran dasar (Flat)'),
('min_trust_score_cod', '10', 'Minimal trust score untuk menggunakan fitur COD');

-- 3. Create Orders table
CREATE TABLE orders (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    requester_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    runner_id UUID REFERENCES users(id) ON DELETE SET NULL,
    item_details TEXT NOT NULL,
    pickup_lat DOUBLE PRECISION NOT NULL,
    pickup_lng DOUBLE PRECISION NOT NULL,
    delivery_lat DOUBLE PRECISION NOT NULL,
    delivery_lng DOUBLE PRECISION NOT NULL,
    estimated_cost DECIMAL(12,2) NOT NULL DEFAULT 0,
    delivery_fee DECIMAL(12,2) NOT NULL DEFAULT 0,
    status VARCHAR(30) NOT NULL DEFAULT 'pending',
    payment_status VARCHAR(30) NOT NULL DEFAULT 'unpaid',
    payment_method VARCHAR(30) NOT NULL DEFAULT 'escrow',
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_orders_requester_id ON orders(requester_id);
CREATE INDEX idx_orders_runner_id ON orders(runner_id);
CREATE INDEX idx_orders_status ON orders(status);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS orders;
DROP TABLE IF EXISTS configs;

ALTER TABLE users 
    DROP COLUMN IF EXISTS role,
    DROP COLUMN IF EXISTS trust_score,
    DROP COLUMN IF EXISTS is_verified,
    DROP COLUMN IF EXISTS last_lat,
    DROP COLUMN IF EXISTS last_lng;
-- +goose StatementEnd
