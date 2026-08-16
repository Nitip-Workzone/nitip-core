package banner

import (
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

type Banner struct {
	bun.BaseModel `bun:"table:banners,alias:b"`

	ID          uuid.UUID  `bun:"id,pk,type:uuid,default:gen_random_uuid()" json:"id"`
	Title       string     `bun:"title,notnull" json:"title"`
	ImageURL    string     `bun:"image_url,notnull" json:"image_url"`
	RedirectURL *string    `bun:"redirect_url" json:"redirect_url,omitempty"`
	IsActive    bool       `bun:"is_active,notnull,default:true" json:"is_active"`
	PromotionID *uuid.UUID `bun:"promotion_id,type:uuid" json:"promotion_id,omitempty"`
	BadgeText   *string    `bun:"badge_text" json:"badge_text,omitempty"`
	CreatedAt   time.Time  `bun:"created_at,notnull,default:current_timestamp" json:"created_at"`
	UpdatedAt   time.Time  `bun:"updated_at,notnull,default:current_timestamp" json:"updated_at"`
}
