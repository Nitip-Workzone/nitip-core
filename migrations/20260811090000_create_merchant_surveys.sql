-- +goose Up
-- +goose StatementBegin
CREATE TABLE merchant_surveys (
    id UUID PRIMARY KEY,
    merchant_id UUID NOT NULL REFERENCES merchants(id) ON DELETE CASCADE,
    monthly_sales_range VARCHAR(50) NOT NULL,
    average_item_price NUMERIC(12, 2) NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_merchant_surveys_merchant_id ON merchant_surveys(merchant_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS merchant_surveys;
-- +goose StatementEnd
