-- +goose Up
-- +goose StatementBegin
-- P1 hardening: trigram indexes for support search + FAQ + fix cod_enabled idempotent

-- Enable pg_trgm extension if not exists (needed for ILIKE %xxx% acceleration)
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- Support tickets: GIN trigram index for title ILIKE and description ILIKE
CREATE INDEX IF NOT EXISTS idx_support_tickets_title_trgm ON support_tickets USING gin (title gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_support_tickets_desc_trgm ON support_tickets USING gin (description gin_trgm_ops);

-- FAQ: Gin trigram for question/answer/keywords
CREATE INDEX IF NOT EXISTS idx_support_faq_question_trgm ON support_faq USING gin (question gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_support_faq_answer_trgm ON support_faq USING gin (answer gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_support_faq_keywords_trgm ON support_faq USING gin (keywords gin_trgm_ops);

-- Support tickets status + assigned + composite for queue queries (prod 512M shared_buffers)
CREATE INDEX IF NOT EXISTS idx_support_tickets_status_created_at ON support_tickets(status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_support_tickets_assigned_status ON support_tickets(assigned_cs_id, status) WHERE assigned_cs_id IS NOT NULL;

-- Cleanup old migrations: cod_enabled should NOT override intentional false if admin set it after initial true.
-- The 20260727200000 migration did ON CONFLICT DO UPDATE SET value='true' WHERE value='false' which would revert intentional false.
-- We keep cod_enabled as is if exists, and only ensure it exists default true.
INSERT INTO configs (key, value, description) VALUES ('cod_enabled', 'true', 'Aktif/nonaktif metode COD (true/false)') ON CONFLICT (key) DO NOTHING;

-- Ensure support_auto_close_days exists
INSERT INTO configs (key, value, description) VALUES ('support_auto_close_days', '7', 'Otomatis tutup tiket resolved setelah N hari (0 = disable)') ON CONFLICT (key) DO NOTHING;

-- Ensure runners_live cleanup: ensure marker table exists? Not needed, but add comment for prod doc
-- runners_live GEO set is ephemeral in redis allkeys-lru, alive marker runner:alive:<id> TTL 10m controls liveness.

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_support_tickets_title_trgm;
DROP INDEX IF EXISTS idx_support_tickets_desc_trgm;
DROP INDEX IF EXISTS idx_support_faq_question_trgm;
DROP INDEX IF EXISTS idx_support_faq_answer_trgm;
DROP INDEX IF EXISTS idx_support_faq_keywords_trgm;
DROP INDEX IF EXISTS idx_support_tickets_status_created_at;
DROP INDEX IF EXISTS idx_support_tickets_assigned_status;
-- +goose StatementEnd
