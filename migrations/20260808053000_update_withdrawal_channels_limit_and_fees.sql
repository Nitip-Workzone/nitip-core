-- +goose Up
UPDATE withdrawal_channels SET min_amount = 50000;
UPDATE withdrawal_channels SET admin_fee_flat = 0 WHERE code IN ('BCA', 'MANDIRI');
UPDATE withdrawal_channels SET admin_fee_flat = 2500 WHERE code IN ('BRI', 'BNI');
UPDATE withdrawal_channels SET admin_fee_flat = 1000 WHERE code IN ('DANA', 'OVO', 'GOPAY', 'SHOPEEPAY');

-- +goose Down
UPDATE withdrawal_channels SET min_amount = 10000;
UPDATE withdrawal_channels SET admin_fee_flat = 2500 WHERE code IN ('BCA', 'MANDIRI', 'BRI', 'BNI');
UPDATE withdrawal_channels SET admin_fee_flat = 1000 WHERE code IN ('DANA', 'OVO', 'GOPAY', 'SHOPEEPAY');
