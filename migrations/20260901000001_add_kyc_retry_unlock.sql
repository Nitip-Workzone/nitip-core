-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS kyc_retry_unlocks (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    target_level VARCHAR(20) NOT NULL CHECK (target_level IN ('separuh','penuh')),
    unlocked_by UUID NOT NULL REFERENCES users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_kyc_retry_unlocks_user_target ON kyc_retry_unlocks(user_id, target_level);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS kyc_retry_unlocks;
-- +goose StatementEnd
