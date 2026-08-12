-- +goose Up
-- +goose StatementBegin
INSERT INTO configs (key, value, description, updated_at) VALUES 
('fee_short_per_km', '300', 'Tarif tambahan per Kilometer untuk pengantaran Instant (Lebih Mahal) dalam Rupiah', NOW())
ON CONFLICT (key) DO UPDATE SET 
    value = EXCLUDED.value,
    description = EXCLUDED.description,
    updated_at = NOW();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM configs WHERE key = 'fee_short_per_km';
-- +goose StatementEnd
