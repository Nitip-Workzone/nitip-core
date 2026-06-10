package systemconfig

import (
	"time"

	"github.com/uptrace/bun"
)

type Config struct {
	bun.BaseModel `bun:"table:configs,alias:c"`

	Key         string    `bun:"key,pk" json:"key"`
	Value       string    `bun:"value,notnull" json:"value"`
	Description string    `bun:"description" json:"description"`
	UpdatedAt   time.Time `bun:"updated_at,nullzero,notnull,default:current_timestamp" json:"updated_at"`
}
