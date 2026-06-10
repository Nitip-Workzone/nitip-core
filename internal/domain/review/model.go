package review

import (
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

type Review struct {
	bun.BaseModel `bun:"table:reviews,alias:r"`

	ID         uuid.UUID `bun:"id,pk,type:uuid" json:"id"`
	OrderID    uuid.UUID `bun:"order_id,type:uuid,notnull" json:"order_id"`
	ReviewerID uuid.UUID `bun:"reviewer_id,type:uuid,notnull" json:"reviewer_id"`
	RevieweeID uuid.UUID `bun:"reviewee_id,type:uuid,notnull" json:"reviewee_id"`
	Rating     int       `bun:"rating,notnull" json:"rating" validate:"min=1,max=5"`
	Comment    string    `bun:"comment,nullzero" json:"comment,omitempty"`
	CreatedAt  time.Time `bun:"created_at,notnull,default:current_timestamp" json:"created_at"`
}
