-- +goose Up
CREATE TABLE withdrawal_channels (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(100) NOT NULL,
    code VARCHAR(50) NOT NULL UNIQUE,
    type VARCHAR(20) NOT NULL, -- BANK, EWALLET, MANUAL
    admin_fee_flat DECIMAL(12, 2) NOT NULL DEFAULT 0,
    admin_fee_percent DECIMAL(5, 2) NOT NULL DEFAULT 0,
    min_amount DECIMAL(12, 2) NOT NULL DEFAULT 10000,
    estimated_time VARCHAR(50) NOT NULL,
    is_active BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Add channel_id and destination_metadata to wallet_transactions
ALTER TABLE wallet_transactions ADD COLUMN channel_id UUID REFERENCES withdrawal_channels(id);
ALTER TABLE wallet_transactions ADD COLUMN destination_metadata JSONB;

-- Seed popular channels based on Duitku pricing (https://www.duitku.com/harga/)
INSERT INTO withdrawal_channels (name, code, type, admin_fee_flat, admin_fee_percent, estimated_time) VALUES
('Bank BCA', 'BCA', 'BANK', 5000, 0, 'Real-time'),
('Bank Mandiri', 'MANDIRI', 'BANK', 5000, 0, 'Real-time'),
('Bank BNI', 'BNI', 'BANK', 5000, 0, 'Real-time'),
('Bank BRI', 'BRI', 'BANK', 5000, 0, 'Real-time'),
('GoPay', 'GOPAY', 'EWALLET', 2500, 0, 'Real-time'),
('OVO', 'OVO', 'EWALLET', 2500, 0, 'Real-time'),
('Dana', 'DANA', 'EWALLET', 2500, 0, 'Real-time'),
('ShopeePay', 'SHOPEEPAY', 'EWALLET', 2500, 0, 'Real-time'),
('Manual Admin', 'MANUAL', 'MANUAL', 500, 0, '1x24 Jam');

-- +goose Down
ALTER TABLE wallet_transactions DROP COLUMN destination_metadata;
ALTER TABLE wallet_transactions DROP COLUMN channel_id;
DROP TABLE withdrawal_channels;
