package merchant

import (
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

type Merchant struct {
	bun.BaseModel `bun:"table:merchants,alias:m"`

	ID              uuid.UUID  `bun:"id,pk,type:uuid" json:"id"`
	OwnerID         uuid.UUID  `bun:"owner_id,type:uuid,notnull" json:"owner_id"`
	Name            string     `bun:"name,notnull" json:"name"`
	Description     string     `bun:"description" json:"description,omitempty"`
	Address         string     `bun:"address" json:"address,omitempty"`
	Latitude        float64    `bun:"latitude,notnull" json:"latitude"`
	Longitude       float64    `bun:"longitude,notnull" json:"longitude"`
	Category        string     `bun:"category,notnull,default:'food'" json:"category"`
	IsOpen          bool       `bun:"is_open,notnull,default:true" json:"is_open"`
	AutoConfirm     bool       `bun:"auto_confirm,notnull,default:false" json:"auto_confirm"`
	MaxActiveOrders int        `bun:"max_active_orders,notnull,default:5" json:"max_active_orders"`
	Rating          float64    `bun:"rating,notnull,default:5.0" json:"rating"`
	CreatedAt       time.Time  `bun:"created_at,nullzero,notnull,default:current_timestamp" json:"created_at"`
	UpdatedAt       time.Time  `bun:"updated_at,nullzero,notnull,default:current_timestamp" json:"updated_at"`
	DeletedAt       *time.Time `bun:"deleted_at,soft_delete,nullzero" json:"deleted_at,omitempty"`
}

type Menu struct {
	bun.BaseModel `bun:"table:menus,alias:mn"`

	ID          uuid.UUID  `bun:"id,pk,type:uuid" json:"id"`
	MerchantID  uuid.UUID  `bun:"merchant_id,type:uuid,notnull" json:"merchant_id"`
	Name        string     `bun:"name,notnull" json:"name"`
	Description string     `bun:"description" json:"description,omitempty"`
	Price       float64    `bun:"price,notnull" json:"price"`
	ImageURL    string     `bun:"image_url" json:"image_url,omitempty"`
	IsAvailable bool       `bun:"is_available,notnull,default:true" json:"is_available"`
	CreatedAt   time.Time  `bun:"created_at,nullzero,notnull,default:current_timestamp" json:"created_at"`
	UpdatedAt   time.Time  `bun:"updated_at,nullzero,notnull,default:current_timestamp" json:"updated_at"`
	DeletedAt   *time.Time `bun:"deleted_at,soft_delete,nullzero" json:"deleted_at,omitempty"`
}

type OrderItem struct {
	bun.BaseModel `bun:"table:order_items,alias:oi"`

	ID              uuid.UUID `bun:"id,pk,type:uuid" json:"id"`
	OrderID         uuid.UUID `bun:"order_id,type:uuid,notnull" json:"order_id"`
	MenuID          uuid.UUID `bun:"menu_id,type:uuid,notnull" json:"menu_id"`
	Quantity        int       `bun:"quantity,notnull" json:"quantity"`
	Notes           string    `bun:"notes" json:"notes,omitempty"`
	PriceAtPurchase float64   `bun:"price_at_purchase,notnull" json:"price_at_purchase"`

	// Virtual fields
	MenuName  string `bun:"-" json:"menu_name,omitempty"`
	MenuImage string `bun:"-" json:"menu_image,omitempty"`
}
