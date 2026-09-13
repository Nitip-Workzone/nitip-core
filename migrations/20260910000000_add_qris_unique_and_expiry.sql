-- +goose Up
-- +goose StatementBegin
ALTER TABLE orders ADD COLUMN IF NOT EXISTS qris_expires_at TIMESTAMPTZ;
-- Partial unique index: ensure active QRIS orders have unique total_payment
-- active = status NOT IN (cancelled, expired, completed) and payment_status='unpaid' and payment_source='qris'
CREATE UNIQUE INDEX IF NOT EXISTS uniq_active_qris_total_payment ON orders (total_payment) WHERE payment_source = 'qris' AND payment_status = 'unpaid' AND status NOT IN ('cancelled','expired','completed');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS uniq_active_qris_total_payment;
ALTER TABLE orders DROP COLUMN IF EXISTS qris_expires_at;
-- +goose StatementEnd

-- Preflight (jalankan manual sebelum migrate up production, harus 0 row untuk aman):
-- SELECT total_payment, COUNT(*) AS duplicate_count, ARRAY_AGG(id) AS order_ids
-- FROM orders WHERE payment_source='qris' AND payment_status='unpaid' AND status NOT IN ('cancelled','expired','completed')
-- GROUP BY total_payment HAVING COUNT(*) > 1;
-- SELECT id, total_payment, payment_status, status FROM orders
-- WHERE payment_source='qris' AND payment_status='unpaid' AND status NOT IN ('cancelled','expired','completed')
-- AND (total_payment IS NULL OR total_payment <= 0);
