-- +goose Up
-- +goose StatementBegin
INSERT INTO configs (key, value, description, updated_at) VALUES 
('runner_min_balance_unverified', '10000', 'Batas saldo minimal dompet untuk kurir non-verifikasi (belum e-KYC) agar dapat menerima pesanan (Rupiah)', NOW()),
('runner_min_balance_verified', '0', 'Batas saldo minimal dompet untuk kurir terverifikasi (sudah e-KYC) agar dapat menerima pesanan (Rupiah)', NOW())
ON CONFLICT (key) DO UPDATE SET 
    value = EXCLUDED.value,
    description = EXCLUDED.description,
    updated_at = NOW();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM configs WHERE key IN ('runner_min_balance_unverified', 'runner_min_balance_verified');
-- +goose StatementEnd
