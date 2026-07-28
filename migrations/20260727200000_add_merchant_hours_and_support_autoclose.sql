-- +goose Up
-- +goose StatementBegin
-- Add opening_hours JSONB to merchants
ALTER TABLE merchants ADD COLUMN IF NOT EXISTS opening_hours JSONB NOT NULL DEFAULT '{}';
ALTER TABLE merchants ADD COLUMN IF NOT EXISTS image_url TEXT;

-- P1 fix: cod_enabled should NOT override admin intentional false. Only insert if not exists default true.
-- Previous version flipped false->true each deploy which is wrong for prod.
INSERT INTO configs (key, value, description) VALUES ('cod_enabled', 'true', 'Aktif/nonaktif metode COD (true/false)') ON CONFLICT (key) DO NOTHING;

-- Support auto-close config
INSERT INTO configs (key, value, description) VALUES ('support_auto_close_days', '7', 'Otomatis tutup tiket resolved setelah N hari (0 = disable)') ON CONFLICT (key) DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE merchants DROP COLUMN IF EXISTS opening_hours;
ALTER TABLE merchants DROP COLUMN IF EXISTS image_url;
DELETE FROM configs WHERE key = 'support_auto_close_days';
-- +goose StatementEnd
