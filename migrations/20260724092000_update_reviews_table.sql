-- +goose Up
-- +goose StatementBegin
ALTER TABLE reviews RENAME COLUMN rating TO runner_rating;
ALTER TABLE reviews RENAME COLUMN comment TO runner_comment;
ALTER TABLE reviews RENAME COLUMN reviewee_id TO runner_id;
ALTER TABLE reviews ADD COLUMN merchant_id UUID REFERENCES merchants(id) ON DELETE SET NULL;
ALTER TABLE reviews ADD COLUMN merchant_rating INT;
ALTER TABLE reviews ADD COLUMN merchant_comment TEXT;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE reviews DROP COLUMN IF EXISTS merchant_comment;
ALTER TABLE reviews DROP COLUMN IF EXISTS merchant_rating;
ALTER TABLE reviews DROP COLUMN IF EXISTS merchant_id;
ALTER TABLE reviews RENAME COLUMN runner_id TO reviewee_id;
ALTER TABLE reviews RENAME COLUMN runner_comment TO comment;
ALTER TABLE reviews RENAME COLUMN runner_rating TO rating;
-- +goose StatementEnd
