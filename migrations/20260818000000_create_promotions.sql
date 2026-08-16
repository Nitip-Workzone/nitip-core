-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS promotions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code VARCHAR(50) NULL,
    merchant_id UUID NULL REFERENCES merchants(id) ON DELETE SET NULL,
    title VARCHAR(255) NOT NULL,
    description TEXT NULL,
    discount_type VARCHAR(10) NOT NULL CHECK (discount_type IN ('flat','percent')),
    discount_value NUMERIC(12,2) NOT NULL CHECK (discount_value > 0),
    budget_total NUMERIC(15,2) NOT NULL CHECK (budget_total > 0),
    budget_used NUMERIC(15,2) NOT NULL DEFAULT 0 CHECK (budget_used >= 0),
    max_uses INT NOT NULL CHECK (max_uses >= 1 AND max_uses <= 10000),
    used_count INT NOT NULL DEFAULT 0 CHECK (used_count >= 0),
    per_user_limit INT NOT NULL DEFAULT 1 CHECK (per_user_limit >= 1),
    first_purchase_only BOOLEAN NOT NULL DEFAULT FALSE,
    discount_scope VARCHAR(20) NOT NULL DEFAULT 'item' CHECK (discount_scope IN ('item','delivery','total')),
    min_order_amount NUMERIC(12,2) NOT NULL DEFAULT 0 CHECK (min_order_amount >= 0),
    auto_apply BOOLEAN NOT NULL DEFAULT FALSE,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    valid_from TIMESTAMPTZ NULL,
    valid_until TIMESTAMPTZ NULL,
    avg_order_value_snapshot NUMERIC(12,2) NULL,
    created_by UUID NULL REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (code IS NULL OR code ~ '^[A-Za-z0-9_-]{3,50}$'),
    CHECK (NOT (auto_apply = true AND code IS NOT NULL)),
    CHECK (discount_type != 'percent' OR discount_value <= 90)
);

CREATE UNIQUE INDEX IF NOT EXISTS uniq_promotions_code_lower ON promotions (LOWER(code)) WHERE code IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_promotions_merchant_active ON promotions (merchant_id, is_active) WHERE is_active = true;
CREATE INDEX IF NOT EXISTS idx_promotions_active_range ON promotions (is_active, valid_until) WHERE is_active = true;
CREATE INDEX IF NOT EXISTS idx_promotions_auto_apply ON promotions (auto_apply, merchant_id) WHERE auto_apply = true AND is_active = true;

CREATE TABLE IF NOT EXISTS promotion_usages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    promotion_id UUID NOT NULL REFERENCES promotions(id) ON DELETE CASCADE,
    order_id UUID NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    merchant_id UUID NULL REFERENCES merchants(id) ON DELETE SET NULL,
    discount_amount NUMERIC(12,2) NOT NULL CHECK (discount_amount >= 0),
    original_amount NUMERIC(12,2) NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (order_id),
    UNIQUE (promotion_id, order_id)
);

CREATE INDEX IF NOT EXISTS idx_promotion_usages_promo_user ON promotion_usages (promotion_id, user_id);
CREATE INDEX IF NOT EXISTS idx_promotion_usages_merchant ON promotion_usages (merchant_id);
CREATE INDEX IF NOT EXISTS idx_promotion_usages_user ON promotion_usages (user_id);
CREATE INDEX IF NOT EXISTS idx_promotion_usages_created ON promotion_usages (created_at DESC);

ALTER TABLE orders ADD COLUMN IF NOT EXISTS promotion_id UUID NULL REFERENCES promotions(id) ON DELETE SET NULL;
ALTER TABLE orders ADD COLUMN IF NOT EXISTS discount_amount NUMERIC(12,2) NOT NULL DEFAULT 0;
ALTER TABLE orders ADD COLUMN IF NOT EXISTS original_total NUMERIC(12,2) NULL;
ALTER TABLE orders ADD COLUMN IF NOT EXISTS discount_type VARCHAR(10) NULL CHECK (discount_type IS NULL OR discount_type IN ('flat','percent'));

CREATE INDEX IF NOT EXISTS idx_orders_promotion ON orders (promotion_id) WHERE promotion_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_orders_promo_merchant ON orders (merchant_id) WHERE discount_amount > 0;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_orders_promo_merchant;
DROP INDEX IF EXISTS idx_orders_promotion;
ALTER TABLE orders DROP COLUMN IF EXISTS discount_type;
ALTER TABLE orders DROP COLUMN IF EXISTS original_total;
ALTER TABLE orders DROP COLUMN IF EXISTS discount_amount;
ALTER TABLE orders DROP COLUMN IF EXISTS promotion_id;
DROP TABLE IF EXISTS promotion_usages;
DROP TABLE IF EXISTS promotions;
-- +goose StatementEnd
