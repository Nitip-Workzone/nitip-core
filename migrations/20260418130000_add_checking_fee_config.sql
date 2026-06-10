-- +goose Up
-- +goose StatementBegin
-- Add order_checking_fee to configs
INSERT INTO configs (key, value, description, updated_at)
VALUES (
    'order_checking_fee', 
    '5000', 
    'Fee paid to runner if requester cancels after price adjustment check (in IDR)', 
    NOW()
) ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM configs WHERE key = 'order_checking_fee';
-- +goose StatementEnd
