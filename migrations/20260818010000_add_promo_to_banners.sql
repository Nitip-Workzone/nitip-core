-- +goose Up
-- +goose StatementBegin
ALTER TABLE banners ADD COLUMN IF NOT EXISTS promotion_id UUID NULL REFERENCES promotions(id) ON DELETE SET NULL;
ALTER TABLE banners ADD COLUMN IF NOT EXISTS badge_text VARCHAR(50) NULL;

CREATE INDEX IF NOT EXISTS idx_banners_promotion ON banners (promotion_id) WHERE promotion_id IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_banners_promotion;
ALTER TABLE banners DROP COLUMN IF EXISTS badge_text;
ALTER TABLE banners DROP COLUMN IF EXISTS promotion_id;
-- +goose StatementEnd
