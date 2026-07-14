-- +goose Up
-- +goose StatementBegin
INSERT INTO configs (key, value, description, updated_at)
VALUES ('platform_fee_percent', '10', 'Persen potongan platform dari biaya pengiriman (service fee runner). Contoh: 10 = 10%', NOW())
ON CONFLICT (key) DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM configs WHERE key = 'platform_fee_percent';
-- +goose StatementEnd