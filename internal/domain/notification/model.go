package notification

import (
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

type Notification struct {
	bun.BaseModel `bun:"table:notifications"`

	ID        uuid.UUID              `json:"id" bun:"id,pk,type:uuid,default:uuid_generate_v4()"`
	UserID    uuid.UUID              `json:"user_id" bun:"user_id,type:uuid"`
	Title     string                 `json:"title" bun:"title"`
	Message   string                 `json:"message" bun:"message"`
	Type      string                 `json:"type" bun:"type"`
	IsRead    bool                   `json:"is_read" bun:"is_read"`
	Metadata  map[string]interface{} `json:"metadata" bun:"metadata,type:jsonb"`
	CreatedAt time.Time              `json:"created_at" bun:"created_at,default:now()"`
}

type CreateNotificationRequest struct {
	UserID   uuid.UUID              `json:"user_id"`
	Title    string                 `json:"title"`
	Message  string                 `json:"message"`
	Type     string                 `json:"type"`
	Metadata map[string]interface{} `json:"metadata"`
}
