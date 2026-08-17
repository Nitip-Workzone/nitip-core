-- +goose Up
-- +goose StatementBegin

-- Addon Master (formerly Topping Master) - independent shared per merchant
-- Bahasa Indonesia: Tambahan - Keju, Bobba, Eskrim bisa dipakai banyak menu
CREATE TABLE IF NOT EXISTS addon_masters (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id UUID NOT NULL REFERENCES merchants(id) ON DELETE CASCADE,
    name VARCHAR(100) NOT NULL,
    image_url VARCHAR(500) NULL,
    sort_order INT NOT NULL DEFAULT 0,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_addon_masters_merchant ON addon_masters(merchant_id);
CREATE UNIQUE INDEX IF NOT EXISTS uniq_addon_masters_merchant_lower_name ON addon_masters(merchant_id, LOWER(name));

CREATE TABLE IF NOT EXISTS addon_options (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    master_id UUID NOT NULL REFERENCES addon_masters(id) ON DELETE CASCADE,
    label VARCHAR(100) NOT NULL,
    price_delta NUMERIC(12,2) NOT NULL DEFAULT 0 CHECK (price_delta >= 0),
    image_url VARCHAR(500) NULL,
    is_available BOOLEAN NOT NULL DEFAULT TRUE,
    sort_order INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_addon_options_master ON addon_options(master_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS addon_options;
DROP TABLE IF EXISTS addon_masters;
-- +goose StatementEnd
