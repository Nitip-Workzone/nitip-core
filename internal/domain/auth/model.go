package auth

import (
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// ApiClient represents a registered client application
type ApiClient struct {
	bun.BaseModel `bun:"table:api_clients,alias:ac"`

	ID            uuid.UUID  `bun:"id,pk,type:uuid,default:gen_random_uuid()" json:"id"`
	AppName       string     `bun:"app_name,notnull" json:"app_name"`
	Platform      string     `bun:"platform,notnull" json:"platform"`
	ApiKey        string     `bun:"api_key,notnull,unique" json:"api_key"`
	ApiSecretHash string     `bun:"api_secret_hash,notnull" json:"-"`
	ApiSecretEnc  string     `bun:"api_secret_enc,notnull" json:"-"`
	IsActive      bool       `bun:"is_active,notnull,default:true" json:"is_active"`
	Description   string     `bun:"description,type:text" json:"description,omitempty"`
	CreatedAt     time.Time  `bun:"created_at,notnull,default:current_timestamp" json:"created_at"`
	UpdatedAt     time.Time  `bun:"updated_at,notnull,default:current_timestamp" json:"updated_at"`
	LastUsedAt    *time.Time `bun:"last_used_at" json:"last_used_at,omitempty"`
}

// GrantToken represents a short-lived anonymous session token
type GrantToken struct {
	bun.BaseModel `bun:"table:grant_tokens,alias:gt"`

	ID          uuid.UUID `bun:"id,pk,type:uuid,default:gen_random_uuid()"`
	ApiClientID uuid.UUID `bun:"api_client_id,notnull,type:uuid"`
	Token       string    `bun:"token,notnull,unique"`
	ExpiresAt   time.Time `bun:"expires_at,notnull"`
	Used        bool      `bun:"used,notnull,default:false"`
	CreatedAt   time.Time `bun:"created_at,notnull,default:current_timestamp"`

	ApiClient *ApiClient `bun:"rel:belongs-to,join:api_client_id=id"`
}

// --- Request/Response DTOs ---

type GrantRequest struct {
	// Empty body - credentials come via headers (X-API-Key, X-API-Secret)
}

type GrantResponse struct {
	GrantToken string    `json:"grant_token"`
	ExpiresAt  time.Time `json:"expires_at"`
}
