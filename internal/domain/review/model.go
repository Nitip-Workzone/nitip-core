package review

import (
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

type Review struct {
	bun.BaseModel `bun:"table:reviews,alias:r"`

	ID              uuid.UUID  `bun:"id,pk,type:uuid" json:"id"`
	OrderID         uuid.UUID  `bun:"order_id,type:uuid,notnull" json:"order_id"`
	ReviewerID      uuid.UUID  `bun:"reviewer_id,type:uuid,notnull" json:"reviewer_id"`
	RunnerID        uuid.UUID  `bun:"runner_id,type:uuid,notnull" json:"runner_id"`
	RunnerRating    int        `bun:"runner_rating,notnull" json:"runner_rating" validate:"min=1,max=5"`
	RunnerComment   string     `bun:"runner_comment,nullzero" json:"runner_comment,omitempty"`
	MerchantID      *uuid.UUID `bun:"merchant_id,type:uuid" json:"merchant_id,omitempty"`
	MerchantRating  *int       `bun:"merchant_rating" json:"merchant_rating,omitempty" validate:"omitempty,min=1,max=5"`
	MerchantComment string     `bun:"merchant_comment,nullzero" json:"merchant_comment,omitempty"`
	CreatedAt       time.Time  `bun:"created_at,notnull,default:current_timestamp" json:"created_at"`
}
