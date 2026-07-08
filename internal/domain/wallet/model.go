package wallet

import (
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

type TransactionType string
type TransactionStatus string

const (
	TypeTopUp         TransactionType = "TOP_UP"
	TypeWithdrawal    TransactionType = "WITHDRAWAL"
	TypeEscrowHold    TransactionType = "ESCROW_HOLD"
	TypeEscrowRelease TransactionType = "ESCROW_RELEASE"
	TypePlatformFee   TransactionType = "PLATFORM_FEE"
	TypeRefund        TransactionType = "REFUND"

	StatusPending   TransactionStatus = "pending"
	StatusCompleted TransactionStatus = "completed"
	StatusFailed    TransactionStatus = "failed"
	StatusRejected  TransactionStatus = "rejected"
)

const (
	SystemUserID   = "00000000-0000-0000-0000-000000000001"
	SystemWalletID = "00000000-0000-0000-0000-000000000001"
)

type Wallet struct {
	bun.BaseModel `bun:"table:wallets,alias:w"`

	ID        uuid.UUID `bun:"id,pk,type:uuid" json:"id"`
	UserID    uuid.UUID `bun:"user_id,type:uuid,notnull" json:"user_id"`
	Balance   float64   `bun:"balance,notnull" json:"balance"`
	CreatedAt time.Time `bun:"created_at,notnull,default:current_timestamp" json:"created_at"`
	UpdatedAt time.Time `bun:"updated_at,notnull,default:current_timestamp" json:"updated_at"`
}

type WithdrawalChannel struct {
	bun.BaseModel `bun:"table:withdrawal_channels,alias:wc"`

	ID              uuid.UUID `bun:"id,pk,type:uuid,default:gen_random_uuid()" json:"id"`
	Name            string    `bun:"name,notnull" json:"name"`
	Code            string    `bun:"code,notnull,unique" json:"code"`
	Type            string    `bun:"type,notnull" json:"type"` // BANK, EWALLET, MANUAL
	AdminFeeFlat    float64   `bun:"admin_fee_flat,notnull,default:0" json:"admin_fee_flat"`
	AdminFeePercent float64   `bun:"admin_fee_percent,notnull,default:0" json:"admin_fee_percent"`
	MinAmount       float64   `bun:"min_amount,notnull,default:10000" json:"min_amount"`
	EstimatedTime   string    `bun:"estimated_time,notnull" json:"estimated_time"`
	IsActive        bool      `bun:"is_active,notnull,default:true" json:"is_active"`
	CreatedAt       time.Time `bun:"created_at,notnull,default:current_timestamp" json:"created_at"`
	UpdatedAt       time.Time `bun:"updated_at,notnull,default:current_timestamp" json:"updated_at"`
}

type WalletTransaction struct {
	bun.BaseModel `bun:"table:wallet_transactions,alias:wt"`

	ID                  uuid.UUID              `bun:"id,pk,type:uuid" json:"id"`
	WalletID            uuid.UUID              `bun:"wallet_id,type:uuid,notnull" json:"wallet_id"`
	OrderID             *uuid.UUID             `bun:"order_id,type:uuid,nullzero" json:"order_id,omitempty"`
	Type                TransactionType        `bun:"type,notnull" json:"type"`
	Amount              float64                `bun:"amount,notnull" json:"amount"`
	Reference           string                 `bun:"reference,nullzero" json:"reference,omitempty"`
	Status              TransactionStatus      `bun:"status,notnull,default:'completed'" json:"status"`
	CreatedAt           time.Time              `bun:"created_at,notnull,default:current_timestamp" json:"created_at"`
	ChannelID           *uuid.UUID             `bun:"channel_id,type:uuid,nullzero" json:"channel_id,omitempty"`
	DestinationMetadata map[string]interface{} `bun:"destination_metadata,type:jsonb,nullzero" json:"destination_metadata,omitempty"`
	QrisString          string                 `bun:"-" json:"qris_string,omitempty"`
	DeeplinkURL         string                 `bun:"-" json:"deeplink_url,omitempty"`
}
