-- +goose Up
-- +goose StatementBegin
CREATE TABLE kyc_submissions (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    id_card_number VARCHAR(50) NOT NULL,
    id_card_image_url TEXT NOT NULL,
    selfie_image_url TEXT NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'pending', -- pending, approved, rejected
    admin_note TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_kyc_user_id ON kyc_submissions(user_id);
CREATE INDEX idx_kyc_status ON kyc_submissions(status);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS kyc_submissions;
-- +goose StatementEnd
