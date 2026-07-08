package chat

import (
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

type ChatMessage struct {
	bun.BaseModel `bun:"table:chat_messages,alias:cm"`

	ID         string    `json:"id" bun:"id,pk,type:uuid,default:gen_random_uuid()"`
	OrderID    uuid.UUID `json:"order_id" bun:"order_id,type:uuid"`
	SenderID   uuid.UUID `json:"sender_id" bun:"sender_id,type:uuid"`
	Content    string    `json:"content" bun:"content"`
	Type       string    `json:"type" bun:"type,default:'text'"` // "text", "image"
	IsRead     bool      `json:"is_read" bun:"is_read,notnull,default:false"`
	CreatedAt  time.Time `json:"created_at" bun:"created_at,nullzero,notnull,default:current_timestamp"`
	SenderRole string    `json:"sender_role" bun:"-"` // "requester" or "runner"
}

type SendMessageRequest struct {
	Content string `json:"content" validate:"required,max=1000"`
}
