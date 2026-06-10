-- +goose Up
-- +goose StatementBegin
ALTER TABLE orders ADD COLUMN dispute_proof_url TEXT;
ALTER TABLE orders ADD COLUMN disputed_at TIMESTAMP WITH TIME ZONE;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE orders DROP COLUMN IF EXISTS dispute_proof_url;
ALTER TABLE orders DROP COLUMN IF EXISTS disputed_at;
-- +goose StatementEnd
