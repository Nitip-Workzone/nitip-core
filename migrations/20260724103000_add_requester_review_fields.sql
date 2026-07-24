-- +goose Up
-- +goose StatementBegin
ALTER TABLE reviews ALTER COLUMN runner_rating DROP NOT NULL;
ALTER TABLE reviews ADD COLUMN requester_id UUID REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE reviews ADD COLUMN requester_rating INT CHECK (requester_rating >= 1 AND requester_rating <= 5);
ALTER TABLE reviews ADD COLUMN requester_comment TEXT;

UPDATE reviews AS r
SET requester_id = o.requester_id
FROM orders AS o
WHERE r.order_id = o.id AND r.requester_id IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM reviews WHERE runner_rating IS NULL;
ALTER TABLE reviews DROP COLUMN IF EXISTS requester_comment;
ALTER TABLE reviews DROP COLUMN IF EXISTS requester_rating;
ALTER TABLE reviews DROP COLUMN IF EXISTS requester_id;
ALTER TABLE reviews ALTER COLUMN runner_rating SET NOT NULL;
-- +goose StatementEnd
