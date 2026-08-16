-- +goose Up
-- +goose StatementBegin
ALTER TABLE kyc_submissions ALTER COLUMN id_card_number DROP NOT NULL;
ALTER TABLE kyc_submissions ALTER COLUMN id_card_image_url DROP NOT NULL;
ALTER TABLE kyc_submissions ADD COLUMN facebook_name VARCHAR(255);
ALTER TABLE kyc_submissions ADD COLUMN facebook_screenshot_url TEXT;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE kyc_submissions DROP COLUMN IF EXISTS facebook_name;
ALTER TABLE kyc_submissions DROP COLUMN IF EXISTS facebook_screenshot_url;
ALTER TABLE kyc_submissions ALTER COLUMN id_card_number SET NOT NULL;
ALTER TABLE kyc_submissions ALTER COLUMN id_card_image_url SET NOT NULL;
-- +goose StatementEnd
