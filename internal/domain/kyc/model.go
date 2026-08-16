package kyc

import (
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

type KycSubmission struct {
	bun.BaseModel `bun:"table:kyc_submissions,alias:ks"`

	ID                    uuid.UUID `bun:"id,pk,type:uuid" json:"id"`
	UserID                uuid.UUID `bun:"user_id,type:uuid" json:"user_id"`
	IdCardNumber          string    `bun:"id_card_number" json:"id_card_number,omitempty"`
	IdCardImageURL        string    `bun:"id_card_image_url" json:"id_card_image_url,omitempty"`
	SelfieImageURL        string    `bun:"selfie_image_url,notnull" json:"selfie_image_url"`
	FacebookName          string    `bun:"facebook_name" json:"facebook_name,omitempty"`
	FacebookScreenshotURL string    `bun:"facebook_screenshot_url" json:"facebook_screenshot_url,omitempty"`
	Status                string    `bun:"status,notnull,default:'pending'" json:"status"`
	AdminNote             string    `bun:"admin_note" json:"admin_note,omitempty"`
	CreatedAt             time.Time `bun:"created_at,nullzero,notnull,default:current_timestamp" json:"created_at"`
	UpdatedAt             time.Time `bun:"updated_at,nullzero,notnull,default:current_timestamp" json:"updated_at"`
}

var (
	StatusPending  = "pending"
	StatusApproved = "approved"
	StatusRejected = "rejected"
)
