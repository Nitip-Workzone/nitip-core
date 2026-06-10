package middleware

import (
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/codecoffy/nitip-core/internal/cache"
	"github.com/codecoffy/nitip-core/pkg/jwt"
	"github.com/codecoffy/nitip-core/pkg/response"
	"github.com/gofiber/fiber/v2"
	"github.com/uptrace/bun"
)

// Protected verifies the JWT token and binds claims to context
func Protected(db *bun.DB, r *cache.Redis) fiber.Handler {
	return func(c *fiber.Ctx) error {
		authHeader := c.Get("Authorization")
		log.Printf("[AUTH_DEBUG] Incoming request: %s %s | AuthHeader: %s", c.Method(), c.Path(), authHeader)

		tokenStr := ""

		if authHeader != "" && strings.HasPrefix(authHeader, "Bearer ") {
			tokenStr = strings.TrimPrefix(authHeader, "Bearer ")
		} else {
			// Fallback to query param for WebSockets
			tokenStr = c.Query("token")
		}

		if tokenStr == "" {
			log.Printf("[AUTH_DEBUG] Denied: Token is empty")
			return response.Unauthorized(c, "missing or invalid authorization")
		}

		claims, err := jwt.ParseToken(tokenStr)
		if err != nil {
			log.Printf("[AUTH_DEBUG] Denied: JWT Parse Error: %v", err)
			return response.Unauthorized(c, "invalid or expired token")
		}

		// --- Session Validation (Token Versioning) ---
		var currentVersion int
		userID := claims.UserID.String()
		cacheKey := fmt.Sprintf("user:session:v:%s", userID)
		cacheHit := false

		// 1. Try Redis first
		if r != nil {
			val, err := r.Get(c.Context(), cacheKey)
			if err == nil && val != "" {
				v, _ := strconv.Atoi(val)
				currentVersion = v
				cacheHit = true
			}
		}

		// 2. Fallback to DB
		if !cacheHit {
			err := db.NewSelect().
				Table("users").
				Column("token_version").
				Where("id = ?", claims.UserID).
				Scan(c.Context(), &currentVersion)

			if err != nil {
				log.Printf("[AUTH_DEBUG] Denied: User/Session not found in DB for ID %s", claims.UserID)
				return response.Unauthorized(c, "sesi tidak ditemukan")
			}

			// Sync back to Redis
			if r != nil {
				_ = r.Set(c.Context(), cacheKey, currentVersion, 24*time.Hour)
			}
		}

		// 3. Compare Version
		if claims.TokenVersion != currentVersion {
			log.Printf("[AUTH_DEBUG] Denied: Version Mismatch for User %s. Claim: %d, DB: %d", claims.UserID, claims.TokenVersion, currentVersion)
			return response.Unauthorized(c, "sesi Anda telah berakhir, silakan login kembali")
		}

		// Inject user claims into Fiber context
		c.Locals("user", claims)
		return c.Next()
	}
}

// Role Middleware
func Role(requiredRoles ...string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		claims, ok := c.Locals("user").(*jwt.CustomClaims)
		if !ok {
			return response.Unauthorized(c, "unauthorized access")
		}

		for _, r := range requiredRoles {
			if claims.Role == r {
				return c.Next()
			}
		}

		return response.Forbidden(c, "you do not have permission to access this resource")
	}
}
