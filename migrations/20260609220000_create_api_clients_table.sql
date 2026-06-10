-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS api_clients (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    app_name VARCHAR(100) NOT NULL,
    platform VARCHAR(50) NOT NULL,
    api_key VARCHAR(64) NOT NULL UNIQUE,
    api_secret_hash VARCHAR(128) NOT NULL,
    api_secret_enc TEXT NOT NULL,
    is_active BOOLEAN NOT NULL DEFAULT true,
    description TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_used_at TIMESTAMPTZ
);

CREATE INDEX idx_api_clients_api_key ON api_clients(api_key) WHERE is_active = true;

CREATE TABLE IF NOT EXISTS grant_tokens (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    api_client_id UUID NOT NULL REFERENCES api_clients(id) ON DELETE CASCADE,
    token VARCHAR(128) NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    used BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_grant_tokens_token ON grant_tokens(token) WHERE used = false;
CREATE INDEX idx_grant_tokens_expires ON grant_tokens(expires_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS grant_tokens;
DROP TABLE IF EXISTS api_clients;
-- +goose StatementEnd