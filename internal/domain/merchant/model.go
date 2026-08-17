package merchant

import (
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

type OpeningHours struct {
	// Map day -> {open: "08:00", close: "22:00"} or closed: true
	Monday    *DayHours `json:"monday,omitempty"`
	Tuesday   *DayHours `json:"tuesday,omitempty"`
	Wednesday *DayHours `json:"wednesday,omitempty"`
	Thursday  *DayHours `json:"thursday,omitempty"`
	Friday    *DayHours `json:"friday,omitempty"`
	Saturday  *DayHours `json:"saturday,omitempty"`
	Sunday    *DayHours `json:"sunday,omitempty"`
}

type DayHours struct {
	Open   string `json:"open"` // e.g. "08:00"
	Close  string `json:"close"`
	Closed bool   `json:"closed,omitempty"`
}

type Merchant struct {
	bun.BaseModel `bun:"table:merchants,alias:m"`

	ID              uuid.UUID    `bun:"id,pk,type:uuid" json:"id"`
	OwnerID         uuid.UUID    `bun:"owner_id,type:uuid,notnull" json:"owner_id"`
	Name            string       `bun:"name,notnull" json:"name"`
	Description     string       `bun:"description" json:"description,omitempty"`
	Address         string       `bun:"address" json:"address,omitempty"`
	Latitude        float64      `bun:"latitude,notnull" json:"latitude"`
	Longitude       float64      `bun:"longitude,notnull" json:"longitude"`
	Category        string       `bun:"category,notnull,default:'food'" json:"category"`
	IsOpen          bool         `bun:"is_open,notnull,default:true" json:"is_open"`
	AutoConfirm     bool         `bun:"auto_confirm,notnull,default:false" json:"auto_confirm"`
	MaxActiveOrders int          `bun:"max_active_orders,notnull,default:5" json:"max_active_orders"`
	Rating          float64      `bun:"rating,notnull,default:5.0" json:"rating"`
	OpeningHours    OpeningHours `bun:"opening_hours,type:jsonb,notnull,default:'{}'" json:"opening_hours"`
	ImageURL        string       `bun:"image_url" json:"image_url,omitempty"`
	CoverURL        string       `bun:"cover_url" json:"cover_url,omitempty"`
	CreatedAt       time.Time    `bun:"created_at,nullzero,notnull,default:current_timestamp" json:"created_at"`
	UpdatedAt       time.Time    `bun:"updated_at,nullzero,notnull,default:current_timestamp" json:"updated_at"`
	DeletedAt       *time.Time   `bun:"deleted_at,soft_delete,nullzero" json:"deleted_at,omitempty"`
}

type MenuCategory struct {
	bun.BaseModel `bun:"table:menu_categories,alias:mc"`
	ID            uuid.UUID `bun:"id,pk,type:uuid" json:"id"`
	MerchantID    uuid.UUID `bun:"merchant_id,type:uuid,notnull" json:"merchant_id"`
	Name          string    `bun:"name,notnull" json:"name"`
	ImageURL      string    `bun:"image_url" json:"image_url,omitempty"`
	SortOrder     int       `bun:"sort_order,notnull,default:0" json:"sort_order"`
	IsActive      bool      `bun:"is_active,notnull,default:true" json:"is_active"`
	CreatedAt     time.Time `bun:"created_at,nullzero,notnull,default:current_timestamp" json:"created_at"`
	UpdatedAt     time.Time `bun:"updated_at,nullzero,notnull,default:current_timestamp" json:"updated_at"`
}

type Menu struct {
	bun.BaseModel `bun:"table:menus,alias:mn"`

	ID          uuid.UUID  `bun:"id,pk,type:uuid" json:"id"`
	MerchantID  uuid.UUID  `bun:"merchant_id,type:uuid,notnull" json:"merchant_id"`
	CategoryID  *uuid.UUID `bun:"category_id,type:uuid" json:"category_id,omitempty"`
	Name        string     `bun:"name,notnull" json:"name"`
	Description string     `bun:"description" json:"description,omitempty"`
	Price       float64    `bun:"price,notnull" json:"price"`
	ImageURL    string     `bun:"image_url" json:"image_url,omitempty"`
	IsAvailable bool       `bun:"is_available,notnull,default:true" json:"is_available"`
	CreatedAt   time.Time  `bun:"created_at,nullzero,notnull,default:current_timestamp" json:"created_at"`
	UpdatedAt   time.Time  `bun:"updated_at,nullzero,notnull,default:current_timestamp" json:"updated_at"`
	DeletedAt   *time.Time `bun:"deleted_at,soft_delete,nullzero" json:"deleted_at,omitempty"`

	// Relations (virtual)
	Category      *MenuCategory      `bun:"rel:belongs-to,join:category_id=id" json:"category,omitempty"`
	VariantGroups []MenuVariantGroup `bun:"rel:has-many,join:id=menu_id" json:"variant_groups,omitempty"`
	ToppingGroups []MenuToppingGroup `bun:"rel:has-many,join:id=menu_id" json:"topping_groups,omitempty"`
}

type MenuVariantGroup struct {
	bun.BaseModel `bun:"table:menu_variant_groups,alias:mvg"`
	ID            uuid.UUID `bun:"id,pk,type:uuid" json:"id"`
	MenuID        uuid.UUID `bun:"menu_id,type:uuid,notnull" json:"menu_id"`
	Name          string    `bun:"name,notnull" json:"name"`
	Type          string    `bun:"type,notnull,default:'single'" json:"type"` // single/multiple
	IsRequired    bool      `bun:"is_required,notnull,default:false" json:"is_required"`
	MinSelect     int       `bun:"min_select,notnull,default:0" json:"min_select"`
	MaxSelect     *int      `bun:"max_select" json:"max_select,omitempty"`
	SortOrder     int       `bun:"sort_order,notnull,default:0" json:"sort_order"`
	CreatedAt     time.Time `bun:"created_at,nullzero,notnull,default:current_timestamp" json:"created_at"`
	UpdatedAt     time.Time `bun:"updated_at,nullzero,notnull,default:current_timestamp" json:"updated_at"`

	Options []MenuVariantOption `bun:"rel:has-many,join:id=group_id" json:"options,omitempty"`
}

type MenuVariantOption struct {
	bun.BaseModel `bun:"table:menu_variant_options,alias:mvo"`
	ID            uuid.UUID `bun:"id,pk,type:uuid" json:"id"`
	GroupID       uuid.UUID `bun:"group_id,type:uuid,notnull" json:"group_id"`
	Label         string    `bun:"label,notnull" json:"label"`
	PriceDelta    float64   `bun:"price_delta,notnull,default:0" json:"price_delta"`
	ImageURL      string    `bun:"image_url" json:"image_url,omitempty"`
	IsDefault     bool      `bun:"is_default,notnull,default:false" json:"is_default"`
	IsAvailable   bool      `bun:"is_available,notnull,default:true" json:"is_available"`
	SortOrder     int       `bun:"sort_order,notnull,default:0" json:"sort_order"`
	CreatedAt     time.Time `bun:"created_at,nullzero,notnull,default:current_timestamp" json:"created_at"`
	UpdatedAt     time.Time `bun:"updated_at,nullzero,notnull,default:current_timestamp" json:"updated_at"`
}

type MenuToppingGroup struct {
	bun.BaseModel   `bun:"table:menu_topping_groups,alias:mtg"`
	ID              uuid.UUID  `bun:"id,pk,type:uuid" json:"id"`
	MenuID          uuid.UUID  `bun:"menu_id,type:uuid,notnull" json:"menu_id"`
	VariantOptionID *uuid.UUID `bun:"variant_option_id,type:uuid" json:"variant_option_id,omitempty"`
	Name            string     `bun:"name,notnull" json:"name"`
	Type            string     `bun:"type,notnull,default:'multiple'" json:"type"`
	IsRequired      bool       `bun:"is_required,notnull,default:false" json:"is_required"`
	MinSelect       int        `bun:"min_select,notnull,default:0" json:"min_select"`
	MaxSelect       *int       `bun:"max_select" json:"max_select,omitempty"`
	SortOrder       int        `bun:"sort_order,notnull,default:0" json:"sort_order"`
	CreatedAt       time.Time  `bun:"created_at,nullzero,notnull,default:current_timestamp" json:"created_at"`
	UpdatedAt       time.Time  `bun:"updated_at,nullzero,notnull,default:current_timestamp" json:"updated_at"`

	Options []MenuToppingOption `bun:"rel:has-many,join:id=group_id" json:"options,omitempty"`
}

type MenuToppingOption struct {
	bun.BaseModel `bun:"table:menu_topping_options,alias:mto"`
	ID            uuid.UUID `bun:"id,pk,type:uuid" json:"id"`
	GroupID       uuid.UUID `bun:"group_id,type:uuid,notnull" json:"group_id"`
	Label         string    `bun:"label,notnull" json:"label"`
	PriceDelta    float64   `bun:"price_delta,notnull,default:0" json:"price_delta"`
	ImageURL      string    `bun:"image_url" json:"image_url,omitempty"`
	IsAvailable   bool      `bun:"is_available,notnull,default:true" json:"is_available"`
	SortOrder     int       `bun:"sort_order,notnull,default:0" json:"sort_order"`
	CreatedAt     time.Time `bun:"created_at,nullzero,notnull,default:current_timestamp" json:"created_at"`
	UpdatedAt     time.Time `bun:"updated_at,nullzero,notnull,default:current_timestamp" json:"updated_at"`
}

type OrderItem struct {
	bun.BaseModel `bun:"table:order_items,alias:oi"`

	ID               uuid.UUID              `bun:"id,pk,type:uuid" json:"id"`
	OrderID          uuid.UUID              `bun:"order_id,type:uuid,notnull" json:"order_id"`
	MenuID           uuid.UUID              `bun:"menu_id,type:uuid,notnull" json:"menu_id"`
	Quantity         int                    `bun:"quantity,notnull" json:"quantity"`
	Notes            string                 `bun:"notes" json:"notes,omitempty"`
	PriceAtPurchase  float64                `bun:"price_at_purchase,notnull" json:"price_at_purchase"`
	Options          map[string]interface{} `bun:"options,type:jsonb" json:"options,omitempty"`
	VariantOptionID  *uuid.UUID             `bun:"variant_option_id,type:uuid" json:"variant_option_id,omitempty"`
	ToppingOptionIDs []uuid.UUID            `bun:"topping_option_ids,array" json:"topping_option_ids,omitempty"`

	// Virtual fields
	MenuName  string `bun:"-" json:"menu_name,omitempty"`
	MenuImage string `bun:"-" json:"menu_image,omitempty"`
}

type MerchantSurvey struct {
	bun.BaseModel `bun:"table:merchant_surveys,alias:ms"`

	ID                uuid.UUID `bun:"id,pk,type:uuid" json:"id"`
	MerchantID        uuid.UUID `bun:"merchant_id,type:uuid,notnull" json:"merchant_id"`
	MonthlySalesRange string    `bun:"monthly_sales_range,notnull" json:"monthly_sales_range"`
	AverageItemPrice  float64   `bun:"average_item_price,notnull" json:"average_item_price"`
	CreatedAt         time.Time `bun:"created_at,nullzero,notnull,default:current_timestamp" json:"created_at"`
}
