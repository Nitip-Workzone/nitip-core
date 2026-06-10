-- +goose Up
-- +goose StatementBegin
INSERT INTO configs (key, value, description, updated_at) VALUES 
('kyc_daily_order_limit', '5', 'Batas maksimal membuat/menerima pesanan per hari untuk akun non-verifikasi', NOW()),
('kyc_daily_withdrawal_limit', '100000', 'Batas maksimal penarikan dana per hari untuk akun non-verifikasi (Rupiah)', NOW()),
('cod_max_amount', '50000', 'Batas maksimal nilai titipan untuk metode pembayaran COD (Rupiah)', NOW()),
('cod_max_distance_km', '10', 'Batas maksimal jarak pengantaran untuk metode pembayaran COD (KM)', NOW())
ON CONFLICT (key) DO UPDATE SET 
    value = EXCLUDED.value,
    description = EXCLUDED.description,
    updated_at = NOW();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM configs WHERE key IN ('kyc_daily_order_limit', 'kyc_daily_withdrawal_limit', 'cod_max_amount', 'cod_max_distance_km');
-- +goose StatementEnd
