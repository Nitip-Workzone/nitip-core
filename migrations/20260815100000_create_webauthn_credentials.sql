-- +goose Up
-- +goose StatementBegin
CREATE TABLE webauthn_credentials (
    id BYTEA PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    public_key BYTEA NOT NULL,
    attestation_type VARCHAR(255) NOT NULL,
    transport VARCHAR(255)[] NOT NULL DEFAULT '{}',
    aaguid BYTEA NOT NULL,
    sign_count BIGINT NOT NULL DEFAULT 0,
    clone_warning BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_webauthn_credentials_user_id ON webauthn_credentials(user_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS webauthn_credentials;
-- +goose StatementEnd
