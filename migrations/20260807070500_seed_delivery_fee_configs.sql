-- +goose Up
-- +goose StatementBegin
INSERT INTO configs (key, value, description, updated_at) VALUES 
('fee_short_base', '3000', 'Biaya dasar awal flat untuk pengantaran Instant dalam Rupiah', NOW()),
('fee_short_per_kg', '2000', 'Tarif tambahan per Kilogram untuk pengantaran Instant dalam Rupiah', NOW()),
('fee_base', '3000', 'Biaya dasar awal flat untuk pengantaran Regular (Hemat Searah) dalam Rupiah', NOW()),
('fee_per_km', '100', 'Tarif tambahan per Kilometer untuk pengantaran Regular (Hemat Searah) dalam Rupiah', NOW()),
('fee_per_kg', '4000', 'Tarif tambahan per Kilogram untuk pengantaran Regular (Hemat Searah) dalam Rupiah', NOW()),
('fee_per_liter', '500', 'Tarif tambahan per Liter volume paket untuk pengantaran Regular (Hemat Searah) dalam Rupiah', NOW())
ON CONFLICT (key) DO UPDATE SET 
    value = EXCLUDED.value,
    description = EXCLUDED.description,
    updated_at = NOW();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM configs WHERE key IN ('fee_short_base', 'fee_short_per_kg', 'fee_base', 'fee_per_km', 'fee_per_kg', 'fee_per_liter');
-- +goose StatementEnd
