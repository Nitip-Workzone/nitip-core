-- +goose Up
INSERT INTO configs (key, value, description, updated_at)
VALUES ('withdrawal_schedule', 'Setiap hari pukul 09:00 WITA', 'Jadwal pemrosesan penarikan dana manual oleh admin', NOW())
ON CONFLICT (key) DO NOTHING;

-- +goose Down
DELETE FROM configs WHERE key = 'withdrawal_schedule';
