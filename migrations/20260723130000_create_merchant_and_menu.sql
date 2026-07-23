-- +goose Up
-- +goose StatementBegin
CREATE TABLE merchants (
    id UUID PRIMARY KEY,
    owner_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    address TEXT,
    latitude DOUBLE PRECISION NOT NULL,
    longitude DOUBLE PRECISION NOT NULL,
    category VARCHAR(50) NOT NULL DEFAULT 'food',
    is_open BOOLEAN NOT NULL DEFAULT TRUE,
    auto_confirm BOOLEAN NOT NULL DEFAULT FALSE,
    max_active_orders INT NOT NULL DEFAULT 5,
    rating NUMERIC(2,1) NOT NULL DEFAULT 5.0,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE menus (
    id UUID PRIMARY KEY,
    merchant_id UUID NOT NULL REFERENCES merchants(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    price NUMERIC(12, 2) NOT NULL,
    image_url TEXT,
    is_available BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE order_items (
    id UUID PRIMARY KEY,
    order_id UUID NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    menu_id UUID NOT NULL REFERENCES menus(id) ON DELETE CASCADE,
    quantity INT NOT NULL,
    notes TEXT,
    price_at_purchase NUMERIC(12, 2) NOT NULL
);

ALTER TABLE orders ADD COLUMN merchant_id UUID REFERENCES merchants(id) ON DELETE SET NULL;

CREATE INDEX idx_merchants_owner_id ON merchants(owner_id);
CREATE INDEX idx_merchants_coordinates ON merchants(latitude, longitude);
CREATE INDEX idx_menus_merchant_id ON menus(merchant_id);
CREATE INDEX idx_order_items_order_id ON order_items(order_id);
CREATE INDEX idx_orders_merchant_id ON orders(merchant_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE orders DROP COLUMN IF EXISTS merchant_id;
DROP TABLE IF EXISTS order_items;
DROP TABLE IF EXISTS menus;
DROP TABLE IF EXISTS merchants;
-- +goose StatementEnd
