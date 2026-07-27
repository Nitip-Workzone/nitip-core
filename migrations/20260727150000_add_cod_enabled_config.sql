-- +goose Up
-- +goose StatementBegin
INSERT INTO configs (key, value, description) VALUES ('cod_enabled', 'false', 'Aktif/nonaktif metode COD (true/false)') ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, description = EXCLUDED.description;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM configs WHERE key = 'cod_enabled';
-- +goose StatementEnd
