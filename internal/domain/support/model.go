package support

import (
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

const (
	StatusOpen        = "open"
	StatusQueued      = "queued"
	StatusAssigned    = "assigned"
	StatusInProgress  = "in_progress"
	StatusWaitingUser = "waiting_user"
	StatusResolved    = "resolved"
	StatusClosed      = "closed"

	CategoryOrderIssue = "order_issue"
	CategoryPayment    = "payment"
	CategoryAccount    = "account"
	CategoryMerchant   = "merchant"
	CategoryOther      = "other"

	SenderRoleUser   = "user"
	SenderRoleCS     = "cs"
	SenderRoleSystem = "system"
)

type Ticket struct {
	bun.BaseModel `bun:"table:support_tickets,alias:st"`

	ID           uuid.UUID  `bun:"id,pk,type:uuid,default:uuid_generate_v4()" json:"id"`
	UserID       uuid.UUID  `bun:"user_id,type:uuid,notnull" json:"user_id"`
	OrderID      *uuid.UUID `bun:"order_id,type:uuid" json:"order_id,omitempty"`
	Category     string     `bun:"category,notnull,default:'other'" json:"category"`
	Title        string     `bun:"title,notnull" json:"title"`
	Description  string     `bun:"description,notnull" json:"description"`
	Status       string     `bun:"status,notnull,default:'queued'" json:"status"`
	Priority     int        `bun:"priority,notnull,default:1" json:"priority"`
	AssignedCSID *uuid.UUID `bun:"assigned_cs_id,type:uuid" json:"assigned_cs_id,omitempty"`
	CreatedAt    time.Time  `bun:"created_at,notnull,default:current_timestamp" json:"created_at"`
	UpdatedAt    time.Time  `bun:"updated_at,notnull,default:current_timestamp" json:"updated_at"`
	ResolvedAt   *time.Time `bun:"resolved_at" json:"resolved_at,omitempty"`
	ClosedAt     *time.Time `bun:"closed_at" json:"closed_at,omitempty"`

	// Virtuals for response enrichment
	UserName       string `bun:"-" json:"user_name,omitempty"`
	UserEmail      string `bun:"-" json:"user_email,omitempty"`
	UserWhatsapp   string `bun:"-" json:"user_whatsapp,omitempty"`
	AssignedCSName string `bun:"-" json:"assigned_cs_name,omitempty"`
	AssignedCSWhatsapp string `bun:"-" json:"assigned_cs_whatsapp,omitempty"`
	OrderNo        string `bun:"-" json:"order_no,omitempty"`
	MessageCount   int    `bun:"-" json:"message_count,omitempty"`
}

type Message struct {
	bun.BaseModel `bun:"table:support_messages,alias:sm"`

	ID         uuid.UUID `bun:"id,pk,type:uuid,default:uuid_generate_v4()" json:"id"`
	TicketID   uuid.UUID `bun:"ticket_id,type:uuid,notnull" json:"ticket_id"`
	SenderID   uuid.UUID `bun:"sender_id,type:uuid,notnull" json:"sender_id"`
	SenderRole string    `bun:"sender_role,notnull,default:'user'" json:"sender_role"`
	Message    string    `bun:"message,notnull" json:"message"`
	IsInternal bool      `bun:"is_internal,notnull,default:false" json:"is_internal"`
	CreatedAt  time.Time `bun:"created_at,notnull,default:current_timestamp" json:"created_at"`

	// Virtual
	SenderName string `bun:"-" json:"sender_name,omitempty"`
}

type FAQ struct {
	bun.BaseModel `bun:"table:support_faq,alias:sf"`

	ID        uuid.UUID `bun:"id,pk,type:uuid,default:uuid_generate_v4()" json:"id"`
	Category  string    `bun:"category,notnull,default:'general'" json:"category"`
	Question  string    `bun:"question,notnull" json:"question"`
	Answer    string    `bun:"answer,notnull" json:"answer"`
	Keywords  string    `bun:"keywords" json:"keywords,omitempty"`
	IsActive  bool      `bun:"is_active,notnull,default:true" json:"is_active"`
	CreatedAt time.Time `bun:"created_at,notnull,default:current_timestamp" json:"created_at"`
	UpdatedAt time.Time `bun:"updated_at,notnull,default:current_timestamp" json:"updated_at"`
}
