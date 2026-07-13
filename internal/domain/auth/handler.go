package auth

import (
	"context"
	"log"
	"time"

	"github.com/codecoffy/nitip-core/pkg/response"
	"github.com/gofiber/fiber/v2"
	"github.com/uptrace/bun"
)

type Handler struct {
	service *Service
}

func NewHandler(db *bun.DB) *Handler {
	return &Handler{service: NewService(db)}
}

func (h *Handler) RegisterRoutes(router fiber.Router) {
	auth := router.Group("/auth")
	auth.Post("/grant", h.Grant)
}

// Grant godoc
// @Summary      Get a grant token
// @Description  Exchange API Key + HMAC Signature for a short-lived grant token (15 min)
// @Tags         Auth
// @Produce      json
// @Param        X-API-Key    header  string  true  "API Key (public identifier)"
// @Param        X-Timestamp  header  string  true  "RFC3339 timestamp (must be within ±5 min)"
// @Param        X-Signature  header  string  true  "HMAC-SHA256(timestamp.sha256(body), api_secret)"
// @Success      200  {object}  response.envelope{data=GrantResponse}
// @Failure      401  {object}  response.envelope
// @Router       /auth/grant [post]
func (h *Handler) Grant(c *fiber.Ctx) error {
	apiKey := c.Get("X-API-Key")
	timestamp := c.Get("X-Timestamp")
	signature := c.Get("X-Signature")

	if apiKey == "" || timestamp == "" || signature == "" {
		return response.Unauthorized(c, "header wajib tidak lengkap: X-API-Key, X-Timestamp, X-Signature")
	}

	// Verify HMAC signature (API secret is never sent over the wire)
	client, err := h.service.ValidateHMAC(c.Context(), apiKey, timestamp, signature, string(c.Body()))
	if err != nil {
		log.Printf("[AUTH_GRANT] Failed: %v", err)
		return response.Unauthorized(c, err.Error())
	}

	// Issue grant token
	grant, err := h.service.CreateGrantToken(c.Context(), client.ID)
	if err != nil {
		log.Printf("[AUTH_GRANT] Failed to create grant token: %v", err)
		return response.InternalError(c, "gagal menerbitkan token akses")
	}

	log.Printf("[AUTH_GRANT] Grant token issued for client: %s (%s)", client.AppName, client.Platform)

	return response.Success(c, "token akses berhasil diterbitkan", GrantResponse{
		GrantToken: grant.Token,
		ExpiresAt:  grant.ExpiresAt,
	})
}

// RequireGrant middleware validates the grant token in X-Grant-Token header
// Used to protect the /auth/login endpoint
func RequireGrant(db *bun.DB) fiber.Handler {
	svc := NewService(db)
	return func(c *fiber.Ctx) error {
		grantToken := c.Get("X-Grant-Token")

		if grantToken == "" {
			return response.Unauthorized(c, "header X-Grant-Token tidak ditemukan. Silakan dapatkan token melalui POST /auth/grant terlebih dahulu")
		}

		if err := svc.ConsumeGrantToken(c.Context(), grantToken); err != nil {
			switch err {
			case ErrGrantTokenExpired:
				return response.Unauthorized(c, "token akses sudah kedaluwarsa. Silakan minta token baru melalui POST /auth/grant")
			case ErrGrantTokenUsed:
				return response.Unauthorized(c, "token akses sudah digunakan. Silakan minta token baru melalui POST /auth/grant")
			default:
				return response.Unauthorized(c, "token akses tidak valid")
			}
		}

		return c.Next()
	}
}

// StartGrantTokenCleanup runs a background goroutine to clean expired grant tokens
func StartGrantTokenCleanup(db *bun.DB, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for range ticker.C {
			svc := NewService(db)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			n, err := svc.CleanupExpiredGrantTokens(ctx)
			cancel()
			if err != nil {
				log.Printf("[AUTH_CLEANUP] Error cleaning grant tokens: %v", err)
			} else if n > 0 {
				log.Printf("[AUTH_CLEANUP] Cleaned up %d expired grant tokens", n)
			}
		}
	}()
}
