package promotion

import (
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

const (
	DiscountTypeFlat    = "flat"
	DiscountTypePercent = "percent"

	ScopeItem     = "item"
	ScopeDelivery = "delivery"
	ScopeTotal    = "total"
)

var codeRegex = regexp.MustCompile(`^[A-Za-z0-9_-]{3,50}$`)

type Promotion struct {
	bun.BaseModel `bun:"table:promotions,alias:p"`

	ID                    uuid.UUID  `bun:"id,pk,type:uuid,default:gen_random_uuid()" json:"id"`
	Code                  *string    `bun:"code" json:"code,omitempty"`
	MerchantID            *uuid.UUID `bun:"merchant_id,type:uuid" json:"merchant_id,omitempty"`
	Title                 string     `bun:"title,notnull" json:"title"`
	Description           string     `bun:"description" json:"description,omitempty"`
	DiscountType          string     `bun:"discount_type,notnull" json:"discount_type"`
	DiscountValue         float64    `bun:"discount_value,notnull" json:"discount_value"`
	BudgetTotal           float64    `bun:"budget_total,notnull" json:"budget_total"`
	BudgetUsed            float64    `bun:"budget_used,notnull,default:0" json:"budget_used"`
	MaxUses               int        `bun:"max_uses,notnull" json:"max_uses"`
	UsedCount             int        `bun:"used_count,notnull,default:0" json:"used_count"`
	PerUserLimit          int        `bun:"per_user_limit,notnull,default:1" json:"per_user_limit"`
	FirstPurchaseOnly     bool       `bun:"first_purchase_only,notnull,default:false" json:"first_purchase_only"`
	DiscountScope         string     `bun:"discount_scope,notnull,default:'item'" json:"discount_scope"`
	MinOrderAmount        float64    `bun:"min_order_amount,notnull,default:0" json:"min_order_amount"`
	AutoApply             bool       `bun:"auto_apply,notnull,default:false" json:"auto_apply"`
	IsActive              bool       `bun:"is_active,notnull,default:true" json:"is_active"`
	ValidFrom             *time.Time `bun:"valid_from" json:"valid_from,omitempty"`
	ValidUntil            *time.Time `bun:"valid_until" json:"valid_until,omitempty"`
	AvgOrderValueSnapshot *float64   `bun:"avg_order_value_snapshot" json:"avg_order_value_snapshot,omitempty"`
	CreatedBy             *uuid.UUID `bun:"created_by,type:uuid" json:"created_by,omitempty"`
	CreatedAt             time.Time  `bun:"created_at,nullzero,notnull,default:current_timestamp" json:"created_at"`
	UpdatedAt             time.Time  `bun:"updated_at,nullzero,notnull,default:current_timestamp" json:"updated_at"`

	// Virtual for response enrichment
	MerchantName       string  `bun:"-" json:"merchant_name,omitempty"`
	BudgetRemaining    float64 `bun:"-" json:"budget_remaining"`
	DiscountFlatEst    float64 `bun:"-" json:"discount_flat_est"`
	DiscountPercentEst float64 `bun:"-" json:"discount_percent_est"`
}

type PromotionUsage struct {
	bun.BaseModel `bun:"table:promotion_usages,alias:pu"`

	ID             uuid.UUID  `bun:"id,pk,type:uuid,default:gen_random_uuid()" json:"id"`
	PromotionID    uuid.UUID  `bun:"promotion_id,type:uuid,notnull" json:"promotion_id"`
	OrderID        uuid.UUID  `bun:"order_id,type:uuid,notnull" json:"order_id"`
	UserID         uuid.UUID  `bun:"user_id,type:uuid,notnull" json:"user_id"`
	MerchantID     *uuid.UUID `bun:"merchant_id,type:uuid" json:"merchant_id,omitempty"`
	DiscountAmount float64    `bun:"discount_amount,notnull" json:"discount_amount"`
	OriginalAmount *float64   `bun:"original_amount" json:"original_amount,omitempty"`
	CreatedAt      time.Time  `bun:"created_at,nullzero,notnull,default:current_timestamp" json:"created_at"`
}

type CreatePromotionRequest struct {
	Title             string     `json:"title" validate:"required,min=3,max=255"`
	Description       string     `json:"description" validate:"omitempty,max=2000"`
	Code              *string    `json:"code" validate:"omitempty,min=3,max=50"`
	MerchantID        *uuid.UUID `json:"merchant_id"`
	DiscountType      string     `json:"discount_type" validate:"required,oneof=flat percent"`
	DiscountValue     float64    `json:"discount_value" validate:"required,gt=0"`
	BudgetTotal       float64    `json:"budget_total" validate:"required,gt=0"`
	MaxUses           int        `json:"max_uses" validate:"required,min=1,max=10000"`
	PerUserLimit      int        `json:"per_user_limit" validate:"omitempty,min=1"`
	FirstPurchaseOnly bool       `json:"first_purchase_only"`
	DiscountScope     string     `json:"discount_scope" validate:"omitempty,oneof=item delivery total"`
	MinOrderAmount    float64    `json:"min_order_amount" validate:"omitempty,min=0"`
	AutoApply         bool       `json:"auto_apply"`
	ValidFrom         *time.Time `json:"valid_from"`
	ValidUntil        *time.Time `json:"valid_until"`

	// Secure verify
	AdminPassword string `json:"admin_password" validate:"required"`
	TotpCode      string `json:"totp_code" validate:"required,len=6,numeric"`
}

type UpdatePromotionRequest struct {
	Title             *string    `json:"title" validate:"omitempty,min=3,max=255"`
	Description       *string    `json:"description" validate:"omitempty,max=2000"`
	Code              *string    `json:"code" validate:"omitempty,min=3,max=50"`
	MerchantID        *uuid.UUID `json:"merchant_id"`
	DiscountType      *string    `json:"discount_type" validate:"omitempty,oneof=flat percent"`
	DiscountValue     *float64   `json:"discount_value" validate:"omitempty,gt=0"`
	BudgetTotal       *float64   `json:"budget_total" validate:"omitempty,gt=0"`
	MaxUses           *int       `json:"max_uses" validate:"omitempty,min=1,max=10000"`
	PerUserLimit      *int       `json:"per_user_limit" validate:"omitempty,min=1"`
	FirstPurchaseOnly *bool      `json:"first_purchase_only"`
	DiscountScope     *string    `json:"discount_scope" validate:"omitempty,oneof=item delivery total"`
	MinOrderAmount    *float64   `json:"min_order_amount" validate:"omitempty,min=0"`
	AutoApply         *bool      `json:"auto_apply"`
	IsActive          *bool      `json:"is_active"`
	ValidFrom         *time.Time `json:"valid_from"`
	ValidUntil        *time.Time `json:"valid_until"`
	AdminPassword     string     `json:"admin_password" validate:"required"`
	TotpCode          string     `json:"totp_code" validate:"required,len=6,numeric"`
}

type DeletePromotionRequest struct {
	AdminPassword string `json:"admin_password" validate:"required"`
	TotpCode      string `json:"totp_code" validate:"required,len=6,numeric"`
}

type ValidatePromotionRequest struct {
	Code          string     `json:"code" validate:"omitempty,min=3,max=50"`
	MerchantID    *uuid.UUID `json:"merchant_id"`
	ItemTotal     float64    `json:"item_total" validate:"min=0"`
	DeliveryTotal float64    `json:"delivery_total" validate:"min=0"`
	Total         float64    `json:"total" validate:"min=0"`
}

type ValidatePromotionResponse struct {
	Valid          bool       `json:"valid"`
	Promotion      *Promotion `json:"promotion,omitempty"`
	DiscountAmount float64    `json:"discount_amount"`
	Message        string     `json:"message,omitempty"`
}

type CalculatePreviewRequest struct {
	BudgetTotal   float64    `json:"budget_total" validate:"required,gt=0"`
	MaxUses       int        `json:"max_uses" validate:"required,min=1,max=10000"`
	MerchantID    *uuid.UUID `json:"merchant_id"`
	DiscountType  string     `json:"discount_type" validate:"omitempty,oneof=flat percent"`
	DiscountValue float64    `json:"discount_value" validate:"omitempty,gt=0"`
}

type CalculatePreviewResponse struct {
	FlatPerOrder  float64 `json:"flat_per_order"`
	AvgOrderValue float64 `json:"avg_order_value"`
	PercentEst    float64 `json:"percent_est"`
	Message       string  `json:"message"`
}

type SettlementItem struct {
	MerchantID     *uuid.UUID `json:"merchant_id"`
	MerchantName   string     `json:"merchant_name"`
	TotalLiability float64    `json:"total_liability"`
	OrderCount     int        `json:"order_count"`
}

type SettlementResponse struct {
	TotalLiability float64          `json:"total_liability"`
	TotalOrders    int              `json:"total_orders"`
	Items          []SettlementItem `json:"items"`
}

func (p *Promotion) ComputeDerived() {
	p.BudgetRemaining = p.BudgetTotal - p.BudgetUsed
	if p.BudgetRemaining < 0 {
		p.BudgetRemaining = 0
	}
	if p.MaxUses > 0 {
		p.DiscountFlatEst = p.BudgetTotal / float64(p.MaxUses)
	}
	if p.AvgOrderValueSnapshot != nil && *p.AvgOrderValueSnapshot > 0 {
		p.DiscountPercentEst = p.DiscountFlatEst / *p.AvgOrderValueSnapshot * 100
		if p.DiscountType == DiscountTypePercent {
			p.DiscountFlatEst = 0
			if p.AvgOrderValueSnapshot != nil {
				p.DiscountPercentEst = p.DiscountValue
			}
		} else {
			if p.AvgOrderValueSnapshot != nil && *p.AvgOrderValueSnapshot > 0 {
				p.DiscountPercentEst = p.DiscountValue / *p.AvgOrderValueSnapshot * 100
			}
		}
	} else {
		if p.DiscountType == DiscountTypePercent {
			p.DiscountPercentEst = p.DiscountValue
		}
	}
}

func SanitizeAndValidateCode(raw *string) (*string, error) {
	if raw == nil {
		return nil, nil
	}
	trimmed := strings.TrimSpace(*raw)
	if trimmed == "" {
		return nil, nil
	}
	if !codeRegex.MatchString(trimmed) {
		return nil, ErrInvalidCodeFormat
	}
	return &trimmed, nil
}

func ComputeFlatPerOrder(budget float64, maxUses int) float64 {
	if maxUses <= 0 {
		return 0
	}
	return budget / float64(maxUses)
}

func ComputePercentFromFlat(flat, avg float64) float64 {
	if avg <= 0 {
		return 0
	}
	return flat / avg * 100
}
