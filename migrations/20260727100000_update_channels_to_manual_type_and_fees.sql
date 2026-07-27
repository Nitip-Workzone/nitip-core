-- +goose Up
UPDATE withdrawal_channels SET type = 'MANUAL';
UPDATE withdrawal_channels SET admin_fee_flat = 2500 WHERE code IN ('BCA', 'MANDIRI', 'BNI', 'BRI');
UPDATE withdrawal_channels SET admin_fee_flat = 1000 WHERE code IN ('GOPAY', 'OVO', 'DANA', 'SHOPEEPAY');

-- +goose Down
UPDATE withdrawal_channels SET type = 'BANK' WHERE code IN ('BCA', 'MANDIRI', 'BNI', 'BRI');
UPDATE withdrawal_channels SET type = 'EWALLET' WHERE code IN ('GOPAY', 'OVO', 'DANA', 'SHOPEEPAY');
UPDATE withdrawal_channels SET type = 'MANUAL' WHERE code = 'MANUAL';
UPDATE withdrawal_channels SET admin_fee_flat = 5000 WHERE code IN ('BCA', 'MANDIRI', 'BNI', 'BRI');
UPDATE withdrawal_channels SET admin_fee_flat = 2500 WHERE code IN ('GOPAY', 'OVO', 'DANA', 'SHOPEEPAY');
