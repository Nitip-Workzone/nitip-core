-- +goose Up
-- +goose StatementBegin
ALTER TABLE orders ADD COLUMN IF NOT EXISTS escalated_at TIMESTAMP WITH TIME ZONE NULL;

INSERT INTO configs (key, value, description, updated_at) VALUES 
('matching_radius_food', '5', 'Radius pencarian Runner terdekat untuk Nitip Food (km)', CURRENT_TIMESTAMP),
('matching_radius_beli', '8', 'Radius pencarian Runner terdekat untuk Nitip Beli (km)', CURRENT_TIMESTAMP),
('matching_radius_kirim', '8', 'Radius pencarian Runner terdekat untuk Nitip Kirim (km)', CURRENT_TIMESTAMP),
('order_auto_cancel_minutes', '15', 'Batas waktu tunggu pembatalan pesanan otomatis (menit)', CURRENT_TIMESTAMP)
ON CONFLICT (key) DO UPDATE SET 
    value = EXCLUDED.value,
    description = EXCLUDED.description,
    updated_at = CURRENT_TIMESTAMP;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM configs WHERE key IN ('matching_radius_food', 'matching_radius_beli', 'matching_radius_kirim', 'order_auto_cancel_minutes');
ALTER TABLE orders DROP COLUMN IF EXISTS escalated_at;
-- +goose StatementEnd
