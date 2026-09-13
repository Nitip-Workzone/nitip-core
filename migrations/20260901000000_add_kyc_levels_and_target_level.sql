-- +goose Up
-- +goose StatementBegin
-- 1. Add kyc_level to users (belum/separuh/penuh)
ALTER TABLE users ADD COLUMN IF NOT EXISTS kyc_level VARCHAR(20) NOT NULL DEFAULT 'belum' CHECK (kyc_level IN ('belum','separuh','penuh'));

-- Backfill from existing is_verified and kyc data
-- penuh: is_verified true AND has approved KTP (non-empty number + image)
UPDATE users SET kyc_level = 'penuh'
WHERE is_verified = true
  AND id IN (
    SELECT user_id FROM kyc_submissions
    WHERE status = 'approved'
      AND id_card_number IS NOT NULL AND id_card_number <> ''
      AND id_card_image_url IS NOT NULL AND id_card_image_url <> ''
  );

-- separuh: remaining is_verified true -> separuh
UPDATE users SET kyc_level = 'separuh'
WHERE is_verified = true AND kyc_level = 'belum';

-- 2. Add target_level to kyc_submissions
ALTER TABLE kyc_submissions ADD COLUMN IF NOT EXISTS target_level VARCHAR(20) NOT NULL DEFAULT 'separuh' CHECK (target_level IN ('separuh','penuh'));

-- Existing rows: if they had KTP data, consider target penuh? Keep separuh default for simplicity.
-- No data migration needed for old rows — they remain separuh.

-- 3. Partial unique index: only one pending per user+target
CREATE UNIQUE INDEX IF NOT EXISTS idx_kyc_one_pending_per_level
  ON kyc_submissions(user_id, target_level) WHERE status = 'pending';

-- 4. Index for rejection counting
CREATE INDEX IF NOT EXISTS idx_kyc_user_target_status ON kyc_submissions(user_id, target_level, status);

-- 5. Keep is_verified in sync via trigger (optional helper)
-- No trigger; service maintains both fields atomically.

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_kyc_one_pending_per_level;
DROP INDEX IF EXISTS idx_kyc_user_target_status;
ALTER TABLE kyc_submissions DROP COLUMN IF EXISTS target_level;
ALTER TABLE users DROP COLUMN IF EXISTS kyc_level;
-- +goose StatementEnd
