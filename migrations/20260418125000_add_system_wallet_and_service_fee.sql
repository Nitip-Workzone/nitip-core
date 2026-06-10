-- +goose Up
-- +goose StatementBegin
-- Add service_fee to orders
ALTER TABLE orders ADD COLUMN IF NOT EXISTS service_fee NUMERIC NOT NULL DEFAULT 0;

-- Ensure system user exists
INSERT INTO users (id, name, email, password, role, is_verified, created_at, updated_at)
VALUES (
    '00000000-0000-0000-0000-000000000001', 
    'System Revenue', 
    'system@nitip.internal', 
    '$2a$10$UnUsedPasswordHashForSystemUser', 
    'admin', 
    true, 
    NOW(), 
    NOW()
) ON CONFLICT (id) DO NOTHING;

-- Ensure system wallet exists
INSERT INTO wallets (id, user_id, balance, created_at, updated_at)
VALUES (
    '00000000-0000-0000-0000-000000000001', 
    '00000000-0000-0000-0000-000000000001', 
    0, 
    NOW(), 
    NOW()
) ON CONFLICT (id) DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM wallets WHERE id = '00000000-0000-0000-0000-000000000001';
DELETE FROM users WHERE id = '00000000-0000-0000-0000-000000000001';
ALTER TABLE orders DROP COLUMN IF EXISTS service_fee;
-- +goose StatementEnd
