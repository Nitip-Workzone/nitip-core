package middleware

import (
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/codecoffy/nitip-core/config"
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
		// Avoid leaking auth header value — only log presence
		hasAuth := authHeader != ""
		if config.App.AppEnv != "production" {
			log.Printf("[AUTH_DEBUG] %s %s hasAuth=%v", c.Method(), c.Path(), hasAuth)
		}

		tokenStr := ""

		if authHeader != "" && strings.HasPrefix(authHeader, "Bearer ") {
			tokenStr = strings.TrimPrefix(authHeader, "Bearer ")
		} else {
			// Fallback to query param for WebSockets
			tokenStr = c.Query("token")
		}

		if tokenStr == "" {
			if config.App.AppEnv != "production" {
				log.Printf("[AUTH_DEBUG] Denied: Token is empty for %s %s", c.Method(), c.Path())
			}
			return response.Unauthorized(c, "token autentikasi tidak ditemukan atau tidak valid")
		}

		claims, err := jwt.ParseToken(tokenStr)
		if err != nil {
			if config.App.AppEnv != "production" {
				log.Printf("[AUTH_DEBUG] Denied: JWT parse failed for %s %s: %v", c.Method(), c.Path(), err)
			}
			return response.UnauthorizedWithCode(c, "token tidak valid atau sudah kedaluwarsa", "SESSION_EXPIRED")
		}

		// --- Session Validation (Token Versioning + suspended/deleted) ---
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

		// 2. Fallback to DB — also fetch is_suspended and deleted_at
		var dbIsSuspended bool
		var dbDeletedAt *string
		if !cacheHit {
			// Use struct scan to get all needed fields
			type sessionRow struct {
				TokenVersion int     `bun:"token_version"`
				IsSuspended  bool    `bun:"is_suspended"`
				DeletedAt    *string `bun:"deleted_at"`
			}
			var row sessionRow
			err := db.NewSelect().
				Table("users").
				Column("token_version", "is_suspended", "deleted_at").
				Where("id = ?", claims.UserID).
				Scan(c.Context(), &row)

			if err != nil {
				if config.App.AppEnv != "production" {
					log.Printf("[AUTH_DEBUG] Denied: User/Session not found in DB for ID %s", claims.UserID)
				}
				return response.UnauthorizedWithCode(c, "sesi tidak ditemukan", "ACCOUNT_NOT_FOUND")
			}
			currentVersion = row.TokenVersion
			dbIsSuspended = row.IsSuspended
			dbDeletedAt = row.DeletedAt

			// Sync back to Redis
			if r != nil {
				_ = r.Set(c.Context(), cacheKey, currentVersion, 24*time.Hour)
			}
		} else {
			// Redis hit: still need to check suspended/deleted from DB (cannot cache securely)
			type suspendRow struct {
				IsSuspended bool    `bun:"is_suspended"`
				DeletedAt   *string `bun:"deleted_at"`
			}
			var srow suspendRow
			if err := db.NewSelect().Table("users").Column("is_suspended", "deleted_at").Where("id = ?", claims.UserID).Scan(c.Context(), &srow); err == nil {
				dbIsSuspended = srow.IsSuspended
				dbDeletedAt = srow.DeletedAt
			}
		}

		// Check deleted
		if dbDeletedAt != nil && *dbDeletedAt != "" {
			return response.UnauthorizedWithCode(c, "akun tidak tersedia", "ACCOUNT_DELETED")
		}
		// Check suspended
		if dbIsSuspended {
			return response.ForbiddenWithCode(c, "akun Anda sedang ditangguhkan. Hubungi admin.", "ACCOUNT_SUSPENDED")
		}

		// 3. Compare Version
		if claims.TokenVersion != currentVersion {
			if config.App.AppEnv != "production" {
				log.Printf("[AUTH_DEBUG] Denied: Version Mismatch for User %s. Claim: %d, DB: %d", claims.UserID, claims.TokenVersion, currentVersion)
			}
			return response.UnauthorizedWithCode(c, "sesi Anda telah berakhir, silakan login kembali", "SESSION_EXPIRED")
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
			return response.Unauthorized(c, "tidak memiliki akses")
		}

		for _, r := range requiredRoles {
			if claims.Role == r {
				return c.Next()
			}
		}

		return response.Forbidden(c, "Anda tidak memiliki izin untuk mengakses halaman ini")
	}
}
