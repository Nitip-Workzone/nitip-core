-- +goose Up
-- +goose StatementBegin
INSERT INTO configs (key, value, description, updated_at) VALUES 
('merchant_fee_tier1_limit', '50000', 'Batas maksimal harga makanan Tier 1 (Rupiah)', NOW()),
('merchant_fee_tier2_limit', '100000', 'Batas maksimal harga makanan Tier 2 (Rupiah)', NOW()),
('merchant_fee_tier1_amount', '1000', 'Biaya layanan flat untuk merchant pada pesanan Tier 1 (Rupiah)', NOW()),
('merchant_fee_tier2_amount', '3000', 'Biaya layanan flat untuk merchant pada pesanan Tier 2 (Rupiah)', NOW()),
('merchant_fee_tier3_amount', '5000', 'Biaya layanan flat untuk merchant pada pesanan Tier 3 (Rupiah)', NOW())
ON CONFLICT (key) DO UPDATE SET 
    value = EXCLUDED.value,
    description = EXCLUDED.description,
    updated_at = NOW();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM configs WHERE key IN (
    'merchant_fee_tier1_limit', 
    'merchant_fee_tier2_limit', 
    'merchant_fee_tier1_amount', 
    'merchant_fee_tier2_amount', 
    'merchant_fee_tier3_amount'
);
-- +goose StatementEnd
