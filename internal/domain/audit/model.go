package audit

import (
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

type AuditLog struct {
	bun.BaseModel `bun:"table:audit_logs,alias:al"`

	ID         uuid.UUID   `bun:"id,pk,type:uuid,default:gen_random_uuid()" json:"id"`
	UserID     *uuid.UUID  `bun:"user_id,type:uuid" json:"user_id"`
	Action     string      `bun:"action,notnull" json:"action"`
	Resource   string      `bun:"resource,notnull" json:"resource"`
	ResourceID string      `bun:"resource_id" json:"resource_id"`
	OldValues  interface{} `bun:"old_values,type:jsonb" json:"old_values,omitempty"`
	NewValues  interface{} `bun:"new_values,type:jsonb" json:"new_values,omitempty"`
	IPAddress  string      `bun:"ip_address" json:"ip_address"`
	UserAgent  string      `bun:"user_agent" json:"user_agent"`
	CreatedAt  time.Time   `bun:"created_at,nullzero,notnull,default:current_timestamp" json:"created_at"`
}

const (
	ActionWalletDeposit     = "WALLET_DEPOSIT"
	ActionWalletWithdrawal  = "WALLET_WITHDRAWAL"
	ActionKYCApproval       = "KYC_APPROVAL"
	ActionKYCRejection      = "KYC_REJECTION"
	ActionUserSuspend       = "USER_SUSPEND"
	ActionUserUnsuspend     = "USER_UNSUSPEND"
	ActionUserVerify        = "USER_VERIFY"
	ActionUserUpdateTrust   = "USER_UPDATE_TRUST"
	ActionOrderCreate       = "ORDER_CREATE"
	ActionOrderAccept       = "ORDER_ACCEPT"
	ActionOrderComplete     = "ORDER_COMPLETE"
	ActionOrderCancel       = "ORDER_CANCEL"
	ActionOrderPurchased    = "ORDER_PURCHASED"
	ActionOrderPickup       = "ORDER_PICKUP"
	ActionOrderUpdate       = "ORDER_UPDATE"
	ActionWithdrawalApprove = "WITHDRAWAL_APPROVE"
)
