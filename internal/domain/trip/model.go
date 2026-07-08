package trip

import (
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

type Trip struct {
	bun.BaseModel `bun:"table:trips,alias:t"`

	ID                    uuid.UUID  `bun:"id,pk,type:uuid" json:"id"`
	RunnerID              uuid.UUID  `bun:"runner_id,type:uuid" json:"runner_id"`
	OriginName            string     `bun:"origin_name,notnull" json:"origin_name"`
	OriginLat             float64    `bun:"origin_lat,notnull" json:"origin_lat"`
	OriginLng             float64    `bun:"origin_lng,notnull" json:"origin_lng"`
	DestinationName       string     `bun:"destination_name,notnull" json:"destination_name"`
	DestinationLat        float64    `bun:"destination_lat,notnull" json:"destination_lat"`
	DestinationLng        float64    `bun:"destination_lng,notnull" json:"destination_lng"`
	DepartureTime         time.Time  `bun:"departure_time,notnull" json:"departure_time"`
	ReturnTime            *time.Time `bun:"return_time" json:"return_time,omitempty"`
	IsRoundTrip           bool       `bun:"is_round_trip,notnull,default:false" json:"is_round_trip"`
	Status                string     `bun:"status,notnull,default:'active'" json:"status"`
	Notes                 string     `bun:"notes" json:"notes,omitempty"`
	MaxWeightKg           float64    `bun:"max_weight_kg,notnull,default:0" json:"max_weight_kg"`
	AvailableWeightKg     float64    `bun:"available_weight_kg,notnull,default:0" json:"available_weight_kg"`
	MaxVolumeLiters       float64    `bun:"max_volume_liters,notnull,default:0" json:"max_volume_liters"`
	AvailableVolumeLiters float64    `bun:"available_volume_liters,notnull,default:0" json:"available_volume_liters"`
	VehicleType           string     `bun:"vehicle_type,notnull,default:'motorcycle'" json:"vehicle_type"`
	AllowedServiceTypes   []string   `bun:"allowed_service_types,array,notnull,default:'{instant,regular}'" json:"allowed_service_types"`
	CreatedAt             time.Time  `bun:"created_at,nullzero,notnull,default:current_timestamp" json:"created_at"`
	UpdatedAt             time.Time  `bun:"updated_at,nullzero,notnull,default:current_timestamp" json:"updated_at"`
}

var (
	StatusActive    = "active"
	StatusStarted   = "started"
	StatusCompleted = "completed"
	StatusCancelled = "cancelled"
)
