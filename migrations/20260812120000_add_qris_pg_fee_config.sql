-- +goose Up
-- +goose StatementBegin
INSERT INTO configs (key, value, description, updated_at)
VALUES ('qris_pg_fee', '0', 'Biaya penanganan / gateway fee flat untuk metode pembayaran QRIS (Rupiah)', NOW())
ON CONFLICT (key) DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM configs WHERE key = 'qris_pg_fee';
-- +goose StatementEnd
