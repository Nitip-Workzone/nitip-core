-- +goose Up
-- +goose StatementBegin
INSERT INTO configs (key, value, description, updated_at) VALUES
('merchant_discovery_radius_km', '10', 'Radius discovery merchant Food dalam km (global, backend source of truth)', NOW())
ON CONFLICT (key) DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM configs WHERE key = 'merchant_discovery_radius_km';
-- +goose StatementEnd
