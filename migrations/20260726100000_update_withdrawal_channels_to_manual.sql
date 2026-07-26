-- +goose Up
UPDATE withdrawal_channels SET estimated_time = 'Max 1x12 Jam';

-- +goose Down
UPDATE withdrawal_channels SET estimated_time = 'Real-time' WHERE code != 'MANUAL';
UPDATE withdrawal_channels SET estimated_time = '1x24 Jam' WHERE code = 'MANUAL';
