-- +goose Up
-- +goose StatementBegin
ALTER TABLE orders 
ADD COLUMN adjusted_cost DOUBLE PRECISION DEFAULT 0,
ADD COLUMN adjustment_reason TEXT,
ADD COLUMN adjustment_status VARCHAR(20) DEFAULT NULL; -- pending, accepted, rejected
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE orders 
DROP COLUMN adjusted_cost,
DROP COLUMN adjustment_reason,
DROP COLUMN adjustment_status;
-- +goose StatementEnd
