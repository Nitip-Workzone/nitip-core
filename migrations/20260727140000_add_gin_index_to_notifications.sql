-- +goose Up
-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_notifications_metadata_gin ON notifications USING GIN (metadata);
CREATE INDEX IF NOT EXISTS idx_notifications_type ON notifications (type);
CREATE INDEX IF NOT EXISTS idx_notifications_user_read ON notifications (user_id, is_read);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_notifications_metadata_gin;
DROP INDEX IF EXISTS idx_notifications_type;
DROP INDEX IF EXISTS idx_notifications_user_read;
-- +goose StatementEnd
