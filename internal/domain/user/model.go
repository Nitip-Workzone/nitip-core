package user

import (
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

type User struct {
	bun.BaseModel `bun:"table:users,alias:u"`

	ID              uuid.UUID  `bun:"id,pk,type:uuid" json:"id"`
	Name            string     `bun:"name,notnull" json:"name"`
	Email           string     `bun:"email,notnull,unique" json:"email"`
	WhatsappNumber  string     `bun:"whatsapp_number,notnull" json:"whatsapp_number"`
	Password        string     `bun:"password,notnull" json:"-"`
	Role            string     `bun:"role,notnull,default:'requester'" json:"role"`
	TrustScore      int        `bun:"trust_score,notnull,default:0" json:"trust_score"`
	IsVerified      bool       `bun:"is_verified,notnull,default:false" json:"is_verified"`
	VerifiedAt      *time.Time `bun:"verified_at" json:"verified_at,omitempty"`
	FcmToken        *string    `bun:"fcm_token" json:"fcm_token,omitempty"`
	AvatarUrl       *string    `bun:"avatar_url" json:"avatar_url,omitempty"`
	DeviceId        *string    `bun:"device_id" json:"-"`
	LastLat         *float64   `bun:"last_lat" json:"last_lat,omitempty"`
	LastLng         *float64   `bun:"last_lng" json:"last_lng,omitempty"`
	HomeLat         *float64   `bun:"home_lat" json:"home_lat,omitempty"`
	HomeLng         *float64   `bun:"home_lng" json:"home_lng,omitempty"`
	HomeAddress     *string    `bun:"home_address" json:"home_address,omitempty"`
	IsSuspended       bool       `bun:"is_suspended,notnull,default:false" json:"is_suspended"`
	SuspendedReason   *string    `bun:"suspended_reason" json:"suspended_reason,omitempty"`
	IsAcceptingOrders bool       `bun:"is_accepting_orders,notnull,default:false" json:"is_accepting_orders"`
	Pin               *string    `bun:"pin" json:"-"`
	HasPin            bool       `bun:"-" json:"has_pin"`
	TokenVersion      int        `bun:"token_version,notnull,default:0" json:"-"`
	CreatedAt         time.Time  `bun:"created_at,nullzero,notnull,default:current_timestamp" json:"created_at"`
	UpdatedAt         time.Time  `bun:"updated_at,nullzero,notnull,default:current_timestamp" json:"updated_at"`
	DeletedAt         *time.Time `bun:"deleted_at,soft_delete,nullzero" json:"deleted_at,omitempty"`
}

func (u *User) ComputeHasPin() {
	u.HasPin = u.Pin != nil && *u.Pin != ""
}

func (u *User) MaskSensitiveData() {
	u.DeviceId = nil
	u.HomeLat = nil
	u.HomeLng = nil
	u.HomeAddress = nil
	
	// Partial mask email (e.g. j***@email.com)
	if u.Email != "" {
		parts := strings.Split(u.Email, "@")
		if len(parts) == 2 && len(parts[0]) > 1 {
			u.Email = string(parts[0][0]) + "***@" + parts[1]
		}
	}
}
