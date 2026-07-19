package order

import (
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

type Order struct {
	bun.BaseModel `bun:"table:orders,alias:o"`

	ID             uuid.UUID  `bun:"id,pk,type:uuid" json:"id"`
	RequesterID    uuid.UUID  `bun:"requester_id,type:uuid" json:"requester_id"`
	RunnerID       *uuid.UUID `bun:"runner_id,type:uuid" json:"runner_id,omitempty"`
	ItemDetails    string     `bun:"item_details,notnull" json:"item_details"`
	PickupLat      float64    `bun:"pickup_lat,notnull" json:"pickup_lat"`
	PickupLng      float64    `bun:"pickup_lng,notnull" json:"pickup_lng"`
	DeliveryLat    float64    `bun:"delivery_lat,notnull" json:"delivery_lat"`
	DeliveryLng    float64    `bun:"delivery_lng,notnull" json:"delivery_lng"`
	EstimatedCost  float64    `bun:"estimated_cost,notnull,default:0" json:"estimated_cost"`
	DeliveryFee    float64    `bun:"delivery_fee,notnull,default:0" json:"delivery_fee"`
	Status         string     `bun:"status,notnull,default:'pending'" json:"status"`
	PaymentStatus  string     `bun:"payment_status,notnull,default:'unpaid'" json:"payment_status"`
	PaymentMethod  string     `bun:"payment_method,notnull,default:'escrow'" json:"payment_method"`
	PaymentSource  string     `bun:"payment_source,notnull,default:'wallet'" json:"payment_source"`
	CODHandlingFee float64    `bun:"cod_handling_fee,notnull,default:0" json:"cod_handling_fee"`
	CreatedAt      time.Time  `bun:"created_at,nullzero,notnull,default:current_timestamp" json:"created_at"`
	UpdatedAt      time.Time  `bun:"updated_at,nullzero,notnull,default:current_timestamp" json:"updated_at"`

	// Phase 2: Proof & Dispute
	ReceiptImageURL  string     `bun:"receipt_image_url,nullzero" json:"receipt_image_url"`
	DeliveryImageURL string     `bun:"delivery_image_url,nullzero" json:"delivery_image_url"`
	DisputeReason    string     `bun:"dispute_reason,nullzero" json:"dispute_reason,omitempty"`
	DisputeProofURL  string     `bun:"dispute_proof_url,nullzero" json:"dispute_proof_url"`
	DisputedAt       *time.Time `bun:"disputed_at" json:"disputed_at,omitempty"`

	// Price Adjustment
	AdjustedCost     float64    `bun:"adjusted_cost,nullzero" json:"adjusted_cost,omitempty"`
	AdjustmentReason string     `bun:"adjustment_reason,nullzero" json:"adjustment_reason,omitempty"`
	AdjustmentStatus string     `bun:"adjustment_status,nullzero" json:"adjustment_status,omitempty"`
	WeightKg         float64    `bun:"weight_kg,notnull,default:0" json:"weight_kg"`
	VolumeLiters     float64    `bun:"volume_liters,notnull,default:0" json:"volume_liters"`
	ServiceFee       float64    `bun:"service_fee,notnull,default:0" json:"service_fee"`
	TripID           *uuid.UUID `bun:"trip_id,type:uuid" json:"trip_id,omitempty"`
	TotalPayment     float64    `bun:"total_payment,notnull,default:0" json:"total_payment"`
	OrderType        string     `bun:"order_type,notnull,default:'regular'" json:"order_type"`
	CheckingFee      float64    `bun:"checking_fee,notnull,default:0" json:"checking_fee"`

	// Recent Pickup & Proximity Matching
	PickupName    string  `bun:"pickup_name" json:"pickup_name,omitempty"`
	PickupAddress string  `bun:"pickup_address" json:"pickup_address,omitempty"`
	DistanceKm    float64 `bun:"distance_km" json:"distance_km"`

	// Nitip Kirim (Package Delivery)
	ServiceCategory string `bun:"service_category,notnull,default:'beli'" json:"service_category"`
	ReceiverName    string `bun:"receiver_name" json:"receiver_name,omitempty"`
	ReceiverPhone   string `bun:"receiver_phone" json:"receiver_phone,omitempty"`
	DeliveryName    string `bun:"delivery_name" json:"delivery_name,omitempty"`
	DeliveryAddress string `bun:"delivery_address" json:"delivery_address,omitempty"`
	CompletionCode  string `bun:"completion_code,nullzero" json:"completion_code,omitempty"`

	// Virtual fields (populated dynamically, not stored in DB)
	RunnerName      string `bun:"-" json:"runner_name,omitempty"`
	RunnerPhone     string `bun:"-" json:"runner_phone,omitempty"`
	FeedbackRating  *int   `bun:"-" json:"feedback_rating,omitempty"`
	FeedbackComment string `bun:"-" json:"feedback_comment,omitempty"`
	QRISData        string `bun:"qris_data,nullzero" json:"qris_data,omitempty"`
}

var (
	// Service Categories
	CategoryBeli  = "beli"
	CategoryKirim = "kirim"

	// Order Status Enum equivalents
	StatusPending    = "pending"
	StatusAccepted   = "accepted"
	StatusPurchasing = "purchasing"
	StatusDelivering = "delivering"
	StatusCompleted  = "completed"
	StatusCancelled  = "cancelled"
	StatusExpired    = "expired"
	StatusDisputed   = "disputed"

	// Adjustment Status
	AdjustmentPending  = "pending"
	AdjustmentAccepted = "accepted"
	AdjustmentRejected = "rejected"

	// Payment Status
	PaymentUnpaid   = "unpaid"
	PaymentEscrow   = "escrow"
	PaymentReleased = "released"
	PaymentRefunded = "refunded"

	// Payment Method
	MethodEscrow = "escrow"
	MethodCOD    = "cod"
)
