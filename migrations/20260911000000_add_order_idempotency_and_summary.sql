-- +goose Up
-- +goose StatementBegin
ALTER TABLE orders ADD COLUMN IF NOT EXISTS idempotency_key UUID;
ALTER TABLE orders ADD COLUMN IF NOT EXISTS idempotency_request_hash TEXT;
-- Unique per requester for Food orders (skip null keys, keep Beli/Kirim backward compat)
CREATE UNIQUE INDEX IF NOT EXISTS uniq_order_idempotency_per_requester ON orders (requester_id, idempotency_key) WHERE idempotency_key IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS uniq_order_idempotency_per_requester;
ALTER TABLE orders DROP COLUMN IF EXISTS idempotency_request_hash;
ALTER TABLE orders DROP COLUMN IF EXISTS idempotency_key;
-- +goose StatementEnd
