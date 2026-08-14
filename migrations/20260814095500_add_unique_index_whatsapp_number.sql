-- Up
CREATE UNIQUE INDEX idx_users_unique_whatsapp ON users(whatsapp_number) WHERE deleted_at IS NULL;

-- Down
DROP INDEX IF EXISTS idx_users_unique_whatsapp;
