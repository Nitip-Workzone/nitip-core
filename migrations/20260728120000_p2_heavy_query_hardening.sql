-- +goose Up
-- +goose StatementBegin
-- P2 heavy query hardening: prevent full scan & panic on prod 512M/200 conn

-- Orders: add covering indexes for hot paths not yet covered
CREATE INDEX IF NOT EXISTS idx_orders_requester_created ON orders(requester_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_orders_runner_created ON orders(runner_id, created_at DESC) WHERE runner_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_orders_status_created ON orders(status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_orders_payment_created ON orders(payment_status, created_at DESC);

-- Notification: unread count per user is called every dashboard open
-- Already have (user_id, is_read) but add partial where is_read=false for fast count
DROP INDEX IF EXISTS idx_notifications_user_read;
CREATE INDEX IF NOT EXISTS idx_notifications_user_unread ON notifications(user_id) WHERE is_read = false;
CREATE INDEX IF NOT EXISTS idx_notifications_user_created ON notifications(user_id, created_at DESC);

-- Audit logs: admin panel list with filters often scans all
CREATE INDEX IF NOT EXISTS idx_audit_logs_created_at ON audit_logs(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_logs_user_action ON audit_logs(user_id, action, created_at DESC);

-- Wallet transactions: history per wallet + order_id lookup for escrow
CREATE INDEX IF NOT EXISTS idx_wallet_tx_wallet_created ON wallet_transactions(wallet_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_wallet_tx_order ON wallet_transactions(order_id) WHERE order_id IS NOT NULL;

-- Support: ensure tickets user_id + created for my tickets list
CREATE INDEX IF NOT EXISTS idx_support_tickets_user_created ON support_tickets(user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_support_tickets_queue ON support_tickets(status, created_at ASC) WHERE status IN ('queued','open');
CREATE INDEX IF NOT EXISTS idx_support_messages_ticket_created ON support_messages(ticket_id, created_at ASC, id ASC);

-- Trips: active trips by runner for capacity check
CREATE INDEX IF NOT EXISTS idx_trips_runner_status ON trips(runner_id, status) WHERE status IN ('active','started');

-- Users email already unique but ensure device_id cleanup index
CREATE INDEX IF NOT EXISTS idx_users_device_id ON users(device_id) WHERE device_id IS NOT NULL;

-- Chat messages if large
CREATE INDEX IF NOT EXISTS idx_chat_messages_order_created ON chat_messages(order_id, created_at DESC);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_orders_requester_created;
DROP INDEX IF EXISTS idx_orders_runner_created;
DROP INDEX IF EXISTS idx_orders_status_created;
DROP INDEX IF EXISTS idx_orders_payment_created;
DROP INDEX IF EXISTS idx_notifications_user_unread;
DROP INDEX IF EXISTS idx_notifications_user_created;
DROP INDEX IF EXISTS idx_audit_logs_created_at;
DROP INDEX IF EXISTS idx_audit_logs_user_action;
DROP INDEX IF EXISTS idx_wallet_tx_wallet_created;
DROP INDEX IF EXISTS idx_wallet_tx_order;
DROP INDEX IF EXISTS idx_support_tickets_user_created;
DROP INDEX IF EXISTS idx_support_tickets_queue;
DROP INDEX IF EXISTS idx_support_messages_ticket_created;
DROP INDEX IF EXISTS idx_trips_runner_status;
DROP INDEX IF EXISTS idx_users_device_id;
DROP INDEX IF EXISTS idx_chat_messages_order_created;
-- +goose StatementEnd
