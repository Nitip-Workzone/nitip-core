-- +goose Up
-- +goose StatementBegin

-- Menu categories per merchant (Makanan, Minuman, etc) + optional icon image
CREATE TABLE IF NOT EXISTS menu_categories (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id UUID NOT NULL REFERENCES merchants(id) ON DELETE CASCADE,
    name VARCHAR(100) NOT NULL,
    image_url VARCHAR(500) NULL,
    sort_order INT NOT NULL DEFAULT 0,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_menu_categories_merchant ON menu_categories(merchant_id);
CREATE UNIQUE INDEX IF NOT EXISTS uniq_menu_categories_merchant_lower_name ON menu_categories(merchant_id, LOWER(name));

-- Add category_id to menus (nullable for minimal impact)
ALTER TABLE menus ADD COLUMN IF NOT EXISTS category_id UUID NULL;
ALTER TABLE menus ADD CONSTRAINT fk_menus_category_id FOREIGN KEY (category_id) REFERENCES menu_categories(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_menus_category ON menus(category_id) WHERE category_id IS NOT NULL;

-- Variant groups per menu (e.g. Ukuran, Rasa)
CREATE TABLE IF NOT EXISTS menu_variant_groups (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    menu_id UUID NOT NULL REFERENCES menus(id) ON DELETE CASCADE,
    name VARCHAR(100) NOT NULL,
    type VARCHAR(20) NOT NULL DEFAULT 'single' CHECK (type IN ('single','multiple')),
    is_required BOOLEAN NOT NULL DEFAULT FALSE,
    min_select INT NOT NULL DEFAULT 0,
    max_select INT NULL,
    sort_order INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_variant_groups_menu ON menu_variant_groups(menu_id);

-- Variant options per group (e.g. Besar +3000, Kecil -2000) + optional image
CREATE TABLE IF NOT EXISTS menu_variant_options (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id UUID NOT NULL REFERENCES menu_variant_groups(id) ON DELETE CASCADE,
    label VARCHAR(100) NOT NULL,
    price_delta NUMERIC(12,2) NOT NULL DEFAULT 0,
    image_url VARCHAR(500) NULL,
    is_default BOOLEAN NOT NULL DEFAULT FALSE,
    is_available BOOLEAN NOT NULL DEFAULT TRUE,
    sort_order INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_variant_options_group ON menu_variant_options(group_id);

-- Topping groups per menu or per variant option (flexible)
CREATE TABLE IF NOT EXISTS menu_topping_groups (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    menu_id UUID NOT NULL REFERENCES menus(id) ON DELETE CASCADE,
    variant_option_id UUID NULL REFERENCES menu_variant_options(id) ON DELETE CASCADE,
    name VARCHAR(100) NOT NULL,
    type VARCHAR(20) NOT NULL DEFAULT 'multiple' CHECK (type IN ('single','multiple')),
    is_required BOOLEAN NOT NULL DEFAULT FALSE,
    min_select INT NOT NULL DEFAULT 0,
    max_select INT NULL,
    sort_order INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_topping_groups_menu ON menu_topping_groups(menu_id);
CREATE INDEX IF NOT EXISTS idx_topping_groups_variant ON menu_topping_groups(variant_option_id) WHERE variant_option_id IS NOT NULL;

-- Topping options per group + optional image
CREATE TABLE IF NOT EXISTS menu_topping_options (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id UUID NOT NULL REFERENCES menu_topping_groups(id) ON DELETE CASCADE,
    label VARCHAR(100) NOT NULL,
    price_delta NUMERIC(12,2) NOT NULL DEFAULT 0 CHECK (price_delta >= 0),
    image_url VARCHAR(500) NULL,
    is_available BOOLEAN NOT NULL DEFAULT TRUE,
    sort_order INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_topping_options_group ON menu_topping_options(group_id);

-- OrderItems add options JSONB snapshot + variant/topping refs for calc
ALTER TABLE order_items ADD COLUMN IF NOT EXISTS options JSONB NULL;
ALTER TABLE order_items ADD COLUMN IF NOT EXISTS variant_option_id UUID NULL;
ALTER TABLE order_items ADD CONSTRAINT fk_order_items_variant_option FOREIGN KEY (variant_option_id) REFERENCES menu_variant_options(id) ON DELETE SET NULL;
ALTER TABLE order_items ADD COLUMN IF NOT EXISTS topping_option_ids UUID[] NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE order_items DROP COLUMN IF EXISTS topping_option_ids;
ALTER TABLE order_items DROP COLUMN IF EXISTS variant_option_id;
ALTER TABLE order_items DROP COLUMN IF EXISTS options;

DROP TABLE IF EXISTS menu_topping_options;
DROP TABLE IF EXISTS menu_topping_groups;
DROP TABLE IF EXISTS menu_variant_options;
DROP TABLE IF EXISTS menu_variant_groups;
ALTER TABLE menus DROP COLUMN IF EXISTS category_id;
DROP TABLE IF EXISTS menu_categories;
-- +goose StatementEnd
