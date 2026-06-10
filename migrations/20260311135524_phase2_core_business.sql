-- +goose Up
-- +goose StatementBegin

-- 1. Create Wallets Table
CREATE TABLE wallets (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    balance DECIMAL(15, 2) NOT NULL DEFAULT 0.00,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(user_id)
);

-- 2. Create Wallet Transactions Table
CREATE TABLE wallet_transactions (
    id UUID PRIMARY KEY,
    wallet_id UUID NOT NULL REFERENCES wallets(id) ON DELETE CASCADE,
    order_id UUID NULL REFERENCES orders(id) ON DELETE SET NULL,
    type VARCHAR(50) NOT NULL, -- TOP_UP, WITHDRAWAL, ESCROW_HOLD, ESCROW_RELEASE, PLATFORM_FEE, REFUND
    amount DECIMAL(15, 2) NOT NULL,
    reference VARCHAR(255) NULL, -- external payment ID, etc
    status VARCHAR(50) NOT NULL DEFAULT 'completed', -- pending, completed, failed
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- 3. Create Reviews Table
CREATE TABLE reviews (
    id UUID PRIMARY KEY,
    order_id UUID NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    reviewer_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    reviewee_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    rating INT NOT NULL CHECK (rating >= 1 AND rating <= 5),
    comment TEXT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(order_id, reviewer_id)
);

-- 4. Alter Orders Table to support Phase 2 Media and Disputes
ALTER TABLE orders 
    ADD COLUMN receipt_image_url VARCHAR(1024) NULL,
    ADD COLUMN delivery_image_url VARCHAR(1024) NULL,
    ADD COLUMN dispute_reason TEXT NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE orders 
    DROP COLUMN receipt_image_url,
    DROP COLUMN delivery_image_url,
    DROP COLUMN dispute_reason;

DROP TABLE reviews;
DROP TABLE wallet_transactions;
DROP TABLE wallets;
-- +goose StatementEnd
