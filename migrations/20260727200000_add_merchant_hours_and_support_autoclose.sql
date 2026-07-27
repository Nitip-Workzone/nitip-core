-- +goose Up
-- +goose StatementBegin
-- Add opening_hours JSONB to merchants
ALTER TABLE merchants ADD COLUMN IF NOT EXISTS opening_hours JSONB NOT NULL DEFAULT '{}';
ALTER TABLE merchants ADD COLUMN IF NOT EXISTS image_url TEXT;

-- Fix cod_enabled default should be true, not false (if previous migration set false, flip to true)
INSERT INTO configs (key, value, description) VALUES ('cod_enabled', 'true', 'Aktif/nonaktif metode COD (true/false)') ON CONFLICT (key) DO UPDATE SET value = 'true' WHERE configs.value = 'false';

-- Support auto-close config
INSERT INTO configs (key, value, description) VALUES ('support_auto_close_days', '7', 'Otomatis tutup tiket resolved setelah N hari (0 = disable)') ON CONFLICT (key) DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE merchants DROP COLUMN IF EXISTS opening_hours;
ALTER TABLE merchants DROP COLUMN IF EXISTS image_url;
DELETE FROM configs WHERE key = 'support_auto_close_days';
-- +goose StatementEnd
