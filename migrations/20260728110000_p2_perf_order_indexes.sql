-- +goose Up
-- +goose StatementBegin
-- P2 max perf: order hot path indexes to prevent full scan on 512M prod + 200 conn
-- runner_id status queries are used every accept + daily limit + active check
CREATE INDEX IF NOT EXISTS idx_orders_runner_status ON orders(runner_id, status) WHERE runner_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_orders_requester_status ON orders(requester_id, status);
CREATE INDEX IF NOT EXISTS idx_orders_status_payment ON orders(status, payment_status) WHERE status IN ('pending','merchant_accepted','accepted','cooking','ready','purchasing','delivering');
CREATE INDEX IF NOT EXISTS idx_orders_created_at_status ON orders(created_at DESC, status);
-- For trip capacity restore + matching
CREATE INDEX IF NOT EXISTS idx_orders_trip_id ON orders(trip_id) WHERE trip_id IS NOT NULL;
-- For merchant dashboard polling (merchant_id present)
CREATE INDEX IF NOT EXISTS idx_orders_merchant_status ON orders(merchant_id, status) WHERE merchant_id IS NOT NULL;

-- For user location heartbeat: last_lat/lng not worth index, but is_accepting_orders + role + suspended composite helps FindNearbyRunners
CREATE INDEX IF NOT EXISTS idx_users_runner_live ON users(role, is_suspended, is_accepting_orders) WHERE role='runner';

-- For wallet escrow race protection already uses balance check, but ensure order_id unique in escrow transactions?
-- Add partial unique for pending escrow hold per order to prevent double hold
CREATE UNIQUE INDEX IF NOT EXISTS idx_wallet_tx_order_hold_unique ON wallet_transactions(order_id) WHERE type='escrow_hold' AND status='completed';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_orders_runner_status;
DROP INDEX IF EXISTS idx_orders_requester_status;
DROP INDEX IF EXISTS idx_orders_status_payment;
DROP INDEX IF EXISTS idx_orders_created_at_status;
DROP INDEX IF EXISTS idx_orders_trip_id;
DROP INDEX IF EXISTS idx_orders_merchant_status;
DROP INDEX IF EXISTS idx_users_runner_live;
DROP INDEX IF EXISTS idx_wallet_tx_order_hold_unique;
-- +goose StatementEnd
