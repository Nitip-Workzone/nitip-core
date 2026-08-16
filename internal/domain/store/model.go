package store

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// Store represents a manually-curated shopping location (tokoh/toko) in the system.
// Items column is reserved for future product listing per store.
type Store struct {
	bun.BaseModel `bun:"table:stores,alias:s"`

	ID          uuid.UUID       `bun:"id,pk,type:uuid,default:gen_random_uuid()" json:"id"`
	Name        string          `bun:"name,notnull"                              json:"name"`
	Address     string          `bun:"address"                                   json:"address,omitempty"`
	Lat         float64         `bun:"lat,notnull"                               json:"lat"`
	Lng         float64         `bun:"lng,notnull"                               json:"lng"`
	Category    string          `bun:"category"                                  json:"category,omitempty"`
	Description string          `bun:"description"                               json:"description,omitempty"`
	ImageURL    string          `bun:"image_url"                                 json:"image_url,omitempty"`
	Items       json.RawMessage `bun:"items,type:jsonb"                          json:"items,omitempty"`
	IsActive    bool            `bun:"is_active,notnull,default:true"            json:"is_active"`
	CreatedAt   time.Time       `bun:"created_at,notnull,default:current_timestamp" json:"created_at"`
	UpdatedAt   time.Time       `bun:"updated_at,notnull,default:current_timestamp" json:"updated_at"`

	// DistanceKm is a computed field — not a DB column, only populated by FindNearby.
	DistanceKm float64 `bun:"-" json:"distance_km,omitempty"`
}
