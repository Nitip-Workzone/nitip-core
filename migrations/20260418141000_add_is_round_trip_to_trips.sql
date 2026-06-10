-- +goose Up
-- +goose StatementBegin
ALTER TABLE trips ADD COLUMN is_round_trip BOOLEAN NOT NULL DEFAULT FALSE;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE trips DROP COLUMN is_round_trip;
-- +goose StatementEnd
